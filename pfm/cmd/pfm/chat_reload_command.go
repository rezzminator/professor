package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/agentrole"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/store"
)

const (
	reloadNewFlag     = "--new"
	reloadHideFlag    = "--hide"
	reloadThenFlag    = "--then"
	reloadSocketFlag  = "--sock"
	reloadModelFlag   = "--model"
	reloadEffortFlag  = "--effort"
	reloadPaneFlag    = "--pane"
	reloadCacheFlag   = "--cache"
	reloadAccountFlag = "--account"
)

// startReloadWorker launches the detached worker (Detach: true) and releases
// it; a test overrides this var to script the launch.
var startReloadWorker = func(argv []string, opts deps.StartOptions) error {
	process, err := obs.Runner(deps.RealRunner{}).Start(context.Background(), argv, opts)
	if err != nil {
		return err
	}
	return process.Release()
}

var displayReloadWorkerFailure = reloadCommandTmux{}.Display

func runChatReloadWithRuntime(
	args []string,
	stdout, stderr io.Writer,
	runtime commandRuntime,
	env paths.Env,
) int {
	env = defaultEnv(env)
	args = normalizeReloadArgs(args)
	if len(args) == 1 && (args[0] == helpFlag || args[0] == "-h") {
		fmt.Fprintln(stdout, reload.Usage)
		return 0
	}
	if err := validateReloadArgs(args); err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: %v\n", err)
		return 2
	}
	resolved := runtime.Paths
	tmux := reloadCommandTmux{launchDir: resolved.TmuxDir}
	callerSock := reloadSocketArgument(args)
	callerPane := reloadPaneArgument(args)
	// Resolve before detaching; the worker has no tmux ancestry to recover.
	socketPath, pane, _, code := reloadTarget(
		context.Background(), callerSock, callerPane, resolved, runtime, tmux, stderr, env,
	)
	if code != 0 {
		return code
	}
	// Refused HERE, where the caller is still reading: the detached worker has
	// no way to identify an OpenCode seat (no session env, no crumb), so
	// scheduling one printed success and then failed where nobody looked.
	if reload.EngineOf(socketPath) == pfmengine.OpenCode {
		fmt.Fprintf(stderr, "pfm chat reload: OpenCode chats cannot be reloaded yet (%s)\n", filepath.Base(socketPath))
		return 2
	}
	if err := os.MkdirAll(resolved.SIDDir, 0o700); err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: create worker log directory: %v\n", err)
		return 1
	}
	logPath := filepath.Join(resolved.SIDDir, "reload-"+filepath.Base(socketPath)+".log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: open worker log: %v\n", err)
		return 1
	}
	defer func() {
		if err := log.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm chat reload: close worker log: %v\n", err)
		}
	}()
	workerArgs := []string{"--config", runtime.Config.Path, internalCommand, reloadRunCommand}
	workerArgs = append(workerArgs, args...)
	if callerSock == "" {
		// Hand the detached worker the ambiently resolved absolute socket.
		workerArgs = append(workerArgs, reloadSocketFlag, socketPath)
	}
	if callerPane == "" {
		// Preserve the scheduler-resolved pane unless the caller supplied one.
		workerArgs = append(workerArgs, reloadPaneFlag, pane)
	}
	argv := append([]string{os.Args[0]}, workerArgs...) // Stdin unset: a detached Start child defaults to /dev/null.
	if err := startReloadWorker(argv, deps.StartOptions{Stdout: log, Stderr: log, Detach: true}); err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: schedule worker: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "pfm chat reload: reload scheduled in place (log %s)\n", logPath)
	return 0
}

func runChatReloadWorker(args []string, stdout, stderr io.Writer) int {
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: load config: %v\n", err)
		return 1
	}
	return runChatReloadWorkerWithRuntime(args, stdout, stderr, runtime, paths.OSEnv{})
}

func announceReloadWorkerFailure(
	args []string,
	workerLog string,
	display func(context.Context, string, string, string) error,
	stderr io.Writer,
) {
	sock, pane := reloadSocketArgument(args), reloadPaneArgument(args)
	if sock == "" || pane == "" {
		fmt.Fprintln(stderr, "pfm chat reload: cannot announce worker failure: socket or pane missing")
		return
	}
	socketPath, err := paths.SocketPath(sock)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: resolve worker failure pane: %v\n", err)
		return
	}
	lines := strings.Split(strings.TrimSpace(workerLog), "\n")
	message := lines[len(lines)-1]
	if err := display(context.Background(), socketPath, pane, message); err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: display worker failure on %s %s: %v\n", sock, pane, err)
	}
}

func reloadWorkerConfigFailure(args []string, err error, stderr io.Writer) int {
	line := fmt.Sprintf("pfm: config: %v", err)
	fmt.Fprintln(stderr, line)
	announceReloadWorkerFailure(normalizeReloadArgs(args), line, displayReloadWorkerFailure, stderr)
	return 1
}

func runChatReloadWorkerWithRuntime(
	args []string,
	stdout, stderr io.Writer,
	runtime commandRuntime,
	env paths.Env,
) (code int) {
	var workerLog bytes.Buffer
	paneTold := false
	workerStderr := io.MultiWriter(stderr, &workerLog)
	stderr = workerStderr
	defer func() {
		if code == 0 || paneTold {
			return
		}
		if workerLog.Len() == 0 {
			fmt.Fprintf(workerStderr, "pfm chat reload: detached worker failed (exit %d)\n", code)
		}
		announceReloadWorkerFailure(args, workerLog.String(), displayReloadWorkerFailure, stderr)
	}()
	env = defaultEnv(env)
	args = normalizeReloadArgs(args)
	if err := validateReloadArgs(args); err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: %v\n", err)
		return 2
	}
	var account, cacheOverride, sock, requestedPane, then, model, effort string
	newSeat := false
	hide := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case reloadNewFlag:
			newSeat = true
		case reloadHideFlag:
			hide = true
		case reloadThenFlag:
			if index+1 >= len(args) {
				fmt.Fprintln(stderr, "pfm chat reload: --then needs a prompt")
				return 2
			}
			index++
			then = flattenThenLine(args[index])
		case reloadSocketFlag:
			if index+1 >= len(args) {
				fmt.Fprintln(stderr, "pfm chat reload: --sock needs a socket")
				return 2
			}
			index++
			sock = args[index]
		case reloadModelFlag:
			if index+1 >= len(args) {
				fmt.Fprintln(stderr, "pfm chat reload: --model needs a model name")
				return 2
			}
			index++
			model = args[index]
		case reloadEffortFlag:
			if index+1 >= len(args) {
				fmt.Fprintln(stderr, "pfm chat reload: --effort needs a level, as in --effort high")
				return 2
			}
			index++
			effort = args[index]
		case reloadPaneFlag:
			// Internal scheduler plumbing; intentionally absent from reload.Usage.
			if index+1 >= len(args) {
				fmt.Fprintln(stderr, "pfm chat reload: --pane needs a pane id")
				return 2
			}
			index++
			requestedPane = args[index]
		case reloadCacheFlag:
			if index+1 >= len(args) || (args[index+1] != "1h" && args[index+1] != "5m") {
				fmt.Fprintln(stderr, "pfm chat reload: --cache must be 1h|5m")
				return 2
			}
			index++
			cacheOverride = args[index]
		case reloadAccountFlag:
			if index+1 >= len(args) {
				fmt.Fprintln(stderr, "pfm chat reload: --account needs an account number, as in --account 2")
				return 2
			}
			index++
			if _, valid := positiveAccount(args[index]); !valid {
				fmt.Fprintf(stderr, "pfm chat reload: --account takes an account NUMBER, not %q\n", args[index])
				return 2
			}
			if account != "" {
				fmt.Fprintln(stderr, "pfm chat reload: account specified twice")
				return 2
			}
			account = args[index]
		default:
			if _, valid := positiveAccount(args[index]); !valid {
				fmt.Fprintf(stderr, "pfm chat reload: %s\n", reloadArgumentHint(args[index]))
				return 2
			}
			if account != "" {
				fmt.Fprintln(stderr, "pfm chat reload: account specified twice")
				return 2
			}
			account = args[index]
		}
	}
	resolved := runtime.Paths
	tmux := reloadCommandTmux{launchDir: resolved.TmuxDir}
	socketPath, pane, paneState, code := reloadTarget(
		context.Background(),
		sock,
		requestedPane,
		resolved,
		runtime,
		tmux,
		stderr,
		env,
	)
	if code != 0 {
		return code
	}
	engine := reload.EngineOf(socketPath)
	id, transcript, err := resolveReloadSession(resolved, runtime.Config, socketPath, pane, sock == "", env)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: %v\n", err)
		return 1
	}
	// Keep the old id for a post-success --hide.
	leftBehind := id
	name := ""
	if newSeat {
		// Keep transcript for CWD, but clear the id so the run starts fresh.
		id = ""
		// The name follows the live pane: reload types /rename into the
		// reborn chat and relabels the session left behind (reload.followName).
		// Claude only — the custom title lives in its transcript; a Codex
		// seat's name is its tmux window, which the respawn keeps.
		if transcript != "" && engine == pfmengine.Claude {
			if name, err = reload.TranscriptTitle(transcript); err != nil {
				fmt.Fprintf(stderr, "pfm chat reload: %v — the reborn chat keeps its auto-name\n", err)
			}
		}
		if hide {
			fmt.Fprintln(
				stdout,
				"pfm chat reload: --new --hide — the reborn chat starts a NEW conversation in this pane; the one left behind is hidden from the picker once the reboot completes",
			)
		} else {
			fmt.Fprintln(
				stdout,
				"pfm chat reload: --new — the reborn chat starts a NEW conversation in this pane (the old one stays resumable)",
			)
		}
	}
	cwd, err := reload.TranscriptCWD(transcript)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: %v; using the live pane directory\n", err)
	}
	if cwd == "" {
		cwd = paneState.CurrentPath
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if info, statErr := os.Stat(cwd); statErr != nil || !info.IsDir() {
		cwd, _ = os.Getwd()
	}
	promptChannel := ""
	promptChannel, err = agentrole.RefreshSeatPrompt(
		engine, resolved.SIDDir, filepath.Base(socketPath), pane, cwd, resolved.Home,
	)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: %v\n", err)
		return 1
	}
	birthAccount, birthCache, err := reload.BirthAccount(
		resolved, runtime.Config, socketPath, leftBehind, paneState, stderr, defaultEnv(env),
	)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: %v\n", err)
		return 1
	}
	kept := account == ""
	if kept {
		account = strconv.Itoa(birthAccount)
		fmt.Fprintf(stdout, "pfm chat reload: no account given — keeping the chat's current account %s\n", account)
	}
	acct, _ := strconv.Atoi(account)
	selected, err := reload.ValidateAccount(runtime.Config, engine, acct)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: %v\n", err)
		return 2
	}
	cache := birthCache
	if cacheOverride != "" {
		cache = cacheOverride == "1h"
	}
	fmt.Fprintf(
		stdout,
		"pfm chat reload: reloading this chat to account %d IN PLACE — it reboots right here under the new account\n",
		acct,
	)
	if then != "" {
		fmt.Fprintln(
			stdout,
			"pfm chat reload: --then queued — the follow-up is typed into the reborn chat once it reaches its prompt",
		)
	}
	options := reload.Options{
		Home:        resolved.Home,
		SIDDir:      resolved.SIDDir,
		ClaudeRoots: resolved.Roots[pfmengine.Claude],
		Delay:       reloadDurationEnv("PFM_RELOAD_DELAY_MS", 1500, env),
		Poll:        reloadDurationEnv("PFM_RELOAD_POLL_MS", 1000, env),
		ExitTries:   reload.ParseIntEnv(paths.OSEnv{}, "PFM_RELOAD_EXIT_TRIES", 20),
		IdleTries:   reload.ParseIntEnv(paths.OSEnv{}, "PFM_RELOAD_IDLE_TRIES", 120),
		ThenTries:   reload.ParseIntEnv(paths.OSEnv{}, "PFM_RELOAD_THEN_TRIES", 900),
	}
	result, err := reload.Run(
		context.Background(),
		reload.Request{
			Engine:        engine,
			SocketPath:    socketPath,
			Pane:          pane,
			New:           newSeat,
			AccountGiven:  !kept,
			CacheGiven:    cacheOverride != "",
			LeftBehind:    leftBehind,
			SessionID:     id,
			Transcript:    transcript,
			CWD:           cwd,
			Account:       acct,
			AccountIDs:    selected.IDs,
			CodexHome:     selected.CodexHome,
			CodexBinary:   selected.CodexBinary,
			CodexYolo:     selected.CodexYolo,
			Cache1H:       cache,
			Then:          then,
			Name:          name,
			Model:         model,
			Effort:        effort,
			PromptChannel: promptChannel,
			Home:          resolved.Home,
			Machine:       runtime.Config,
		},
		options,
		tmux,
		reload.NewProcessTable(resolved.ProcRoot),
		stderr,
	)
	if err != nil {
		paneTold = reload.PaneTold(err)
		fmt.Fprintf(stderr, "pfm chat reload: %v\n", err)
		return 1
	}
	leftBehind = result.LeftBehind
	if result.New {
		if newSeat {
			fmt.Fprintln(stdout, "pfm chat reload: rebooted FRESH as requested:", filepath.Base(socketPath), pane)
		} else {
			fmt.Fprintln(stdout, "pfm chat reload: no transcript yet — rebooted FRESH")
		}
	} else {
		fmt.Fprintf(stdout, "pfm chat reload: respawned in place: %s %s\n", filepath.Base(socketPath), pane)
	}
	if newSeat && hide {
		// Hide the session Run says this pane left behind.
		if leftBehind == "" {
			fmt.Fprintln(
				stdout,
				"pfm chat reload: --hide — nothing to hide, the conversation left behind had no transcript yet",
			)
			return 0
		}
		if engine == pfmengine.Codex {
			transcript, err = reload.SessionTranscript(resolved, runtime.Config, engine, leftBehind)
			if err != nil {
				fmt.Fprintf(stderr, "pfm chat reload: find conversation to hide: %v\n", err)
				return 1
			}
		}
		hidden, err := hideReloadedConversation(context.Background(), runtime, engine, leftBehind, transcript, stderr)
		if err != nil {
			fmt.Fprintf(
				stderr,
				"pfm chat reload: rebooted fresh, but the conversation left behind is NOT hidden: %v — run: pfm chat kill %s\n",
				err,
				leftBehind,
			)
			return 1
		}
		fmt.Fprintf(
			stdout,
			"pfm chat reload: hid the conversation left behind (%s) — pfm chat unkill %s brings it back\n",
			hidden,
			hidden,
		)
	}
	return 0
}

func reloadSocketArgument(args []string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == reloadSocketFlag {
			return args[index+1]
		}
	}
	return ""
}

// reloadPaneArgument reads the worker pane used to disambiguate a server.
func reloadPaneArgument(args []string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == reloadPaneFlag {
			return args[index+1]
		}
	}
	return ""
}

// flattenThenLine collapses embedded newlines to spaces. deliverThen types
// request.Then into the reborn pane with a single literal tmux send-keys -l
// call (reload.go); a raw newline byte in that stream lands in the pane
// exactly like an Enter keypress, submitting the composer mid-prompt. Both
// Every operator-supplied --then value goes through this flattening, so the
// command has one definition of "one line" for this delivery channel.
func flattenThenLine(text string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(text)
}

func positiveAccount(value string) (int, bool) {
	account, err := strconv.Atoi(value)
	return account, err == nil && account > 0
}

func reloadDurationEnv(name string, fallbackMS int, env paths.Env) time.Duration {
	env = defaultEnv(env)
	if raw, present := env.Lookup(name); present {
		if value, err := strconv.Atoi(raw); err == nil && value >= 0 {
			if value == 0 {
				return -1
			}
			return time.Duration(value) * time.Millisecond
		}
	}
	return time.Duration(fallbackMS) * time.Millisecond
}

// reloadTarget resolves the (socket, pane) a reload acts on. pane is the
// worker-only escape hatch: when the scheduler in runChatReloadWithRuntime
// already knows which pane called it, it hands that pane straight to the
// detached worker via --pane, and this function selects that exact pane out
// of ListPanes instead of falling back to the "exactly one pane" rule below.
// pane is always "" for the scheduler's own call (it has nothing to hand
// itself) and for the ambient-identity branch beneath this one, which never
// takes --pane at all — only an explicit --sock server can carry more than
// one live pane.
func reloadTarget(
	ctx context.Context,
	sock, pane string,
	resolved paths.Values,
	runtime commandRuntime,
	tmux reload.Tmux,
	stderr io.Writer,
	env paths.Env,
) (string, string, reload.Pane, int) {
	env = defaultEnv(env)
	if sock != "" {
		path := sock
		if !filepath.IsAbs(path) {
			path = filepath.Join(resolved.TmuxDir, path)
		}
		panes, err := tmux.ListPanes(ctx, path)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat reload: no live server on %s: %v\n", sock, err)
			return "", "", reload.Pane{}, 1
		}
		if len(panes) == 0 {
			fmt.Fprintf(stderr, "pfm chat reload: no live panes on %s\n", sock)
			return "", "", reload.Pane{}, 1
		}
		if pane != "" {
			for _, item := range panes {
				if item.ID == pane {
					return path, pane, item, 0
				}
			}
			fmt.Fprintf(stderr, "pfm chat reload: pane %s is not live on %s\n", pane, sock)
			return "", "", reload.Pane{}, 1
		}
		if len(panes) != 1 {
			fmt.Fprintf(stderr, "pfm chat reload: %s has multiple panes — run reload inside the chat instead\n", sock)
			return "", "", reload.Pane{}, 1
		}
		return path, panes[0].ID, panes[0], 0
	}
	identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: %v\n", err)
		return "", "", reload.Pane{}, 1
	}
	identity, err := identifier.Identify(ctx)
	if err != nil {
		recovered, found := pfmchat.SeatIdentity(ctx, &runtime)
		if !found {
			fmt.Fprintf(stderr, "pfm chat reload: couldn't identify this chat: %v\n", err)
			return "", "", reload.Pane{}, 1
		}
		identity = recovered
	}
	return reloadTargetFromIdentity(ctx, identity, tmux, stderr, env)
}

func reloadTargetFromIdentity(
	ctx context.Context,
	identity resolve.Identity,
	tmux reload.Tmux,
	stderr io.Writer,
	env paths.Env,
) (string, string, reload.Pane, int) {
	env = defaultEnv(env)
	path := identity.SocketPath
	pane := identity.Pane
	if pane == "" {
		pane = env.Get("TMUX_PANE")
	}
	panes, err := tmux.ListPanes(ctx, path)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: list panes: %v\n", err)
		return "", "", reload.Pane{}, 1
	}
	if pane == "" {
		if len(panes) == 1 {
			return path, panes[0].ID, panes[0], 0
		}
		fmt.Fprintf(
			stderr,
			"pfm chat reload: recovered %s but found %d panes — target is ambiguous\n",
			identity.Session,
			len(panes),
		)
		return "", "", reload.Pane{}, 1
	}
	for _, item := range panes {
		if item.ID == pane {
			return path, pane, item, 0
		}
	}
	fmt.Fprintf(stderr, "pfm chat reload: pane %s is not live\n", pane)
	return "", "", reload.Pane{}, 1
}

func resolveReloadSession(
	resolved paths.Values,
	machine pfmconfig.Config,
	socketPath, pane string,
	allowAmbient bool,
	env paths.Env,
) (string, string, error) {
	env = defaultEnv(env)
	id, crumbPath, err := reload.SessionFromCrumb(
		resolved.SIDDir,
		filepath.Base(socketPath),
		pane,
	)
	if err != nil {
		return "", "", err
	}
	transcript := ""
	if id != "" && !fleet.ChatIDPattern.MatchString(id) {
		return "", "", fmt.Errorf("couldn't identify this chat from breadcrumb %q", crumbPath)
	}
	if crumbPath != "" {
		info, err := os.Stat(crumbPath)
		switch {
		case err == nil && info.Mode().IsRegular():
			transcript = crumbPath
		case err == nil:
			return "", "", fmt.Errorf("chat breadcrumb transcript is not a regular file: %s", crumbPath)
		case !errors.Is(err, fs.ErrNotExist):
			return "", "", fmt.Errorf("stat chat breadcrumb transcript %q: %w", crumbPath, err)
		}
	}
	engine := reload.EngineOf(socketPath)
	if id == "" && allowAmbient {
		ambient := env.Get("CLAUDE_CODE_SESSION_ID")
		if engine == pfmengine.Codex {
			ambient = env.Get("CODEX_THREAD_ID")
		}
		if fleet.ChatIDPattern.MatchString(ambient) {
			path, err := reload.SessionTranscript(resolved, machine, engine, ambient)
			if err != nil {
				return "", "", err
			}
			if path != "" {
				id, transcript = ambient, path
			}
		}
	}
	if id == "" && engine == pfmengine.Codex {
		id, err = resolveReloadCodexPaneBinding(resolved, machine, socketPath, pane)
		if err != nil {
			return "", "", err
		}
	}
	if id == "" {
		return "", "", errors.New("couldn't identify this chat — run the statusline before reloading")
	}
	if transcript == "" {
		path, err := reload.SessionTranscript(resolved, machine, engine, id)
		if err != nil {
			return "", "", err
		}
		transcript = path
	}
	if transcript == "" {
		// A breadcrumb proves which chat this is even before its first
		// transcript record exists. Nothing can be resumed yet, so reboot it
		// fresh without discarding or borrowing another seat's identity.
		return "", "", nil
	}
	return id, transcript, nil
}

func resolveReloadCodexPaneBinding(
	resolved paths.Values,
	machine pfmconfig.Config,
	socketPath, pane string,
) (id string, err error) {
	database, err := store.Open()
	if err != nil {
		return "", fmt.Errorf("open fleet store for Codex reload identity: %w", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close fleet store after Codex reload identity: %w", closeErr))
		}
	}()

	manager, err := kill.New(database, fleet.KillDependencies(commandRuntime{
		Config: machine,
		Paths:  resolved,
	}))
	if err != nil {
		return "", fmt.Errorf("initialize Codex reload identity resolver: %w", err)
	}
	id, found, err := manager.CodexPaneBinding(
		context.Background(),
		filepath.Base(socketPath),
		pane,
	)
	if err != nil {
		return "", fmt.Errorf("read Codex pane binding for %s %s: %w", filepath.Base(socketPath), pane, err)
	}
	if !found {
		return "", nil
	}
	if !fleet.ChatIDPattern.MatchString(id) {
		return "", fmt.Errorf(
			"pane binding for Codex at %s %s is not a valid thread id",
			filepath.Base(socketPath),
			pane,
		)
	}
	return id, nil
}

// hideReloadedConversation records a permanent kill for the conversation a
// `--new --hide` reload left behind, so the picker stops listing it as a
// resumable row. It runs only AFTER reload.Run reported the reboot complete —
// a failed reload leaves the OLD chat live in the pane, and a live chat must
// never be hidden by the command that failed to replace it. The kill goes
// through the same manager as `pfm chat kill <id>` (one writer for the killed
// store, K3): the pane's socket vouches for the engine, so an id the index has
// not caught up with is still hidden, and for Codex the rollout path lets an
// unindexed lineage member resolve to its root the way the picker's own ⌃X
// does. A permanent kill, never a /clear prompt baseline: the reborn pane's
// first prompt must not resurrect the row it replaced. Returns the id the
// kill was recorded under (the lineage root for Codex).
func hideReloadedConversation(
	ctx context.Context,
	runtime commandRuntime,
	engine pfmengine.ID,
	id, transcript string,
	stderr io.Writer,
) (hidden string, err error) {
	if id == "" {
		return "", errors.New("hide needs the id of the conversation left behind")
	}
	database, manager, code := fleet.OpenKillManager(stderr, runtime)
	if code != 0 {
		return "", fmt.Errorf("open the fleet store to hide %s (exit %d, cause above)", id, code)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close fleet store after hiding %s: %w", id, closeErr))
		}
	}()
	rolloutPath := ""
	if engine == pfmengine.Codex {
		rolloutPath = transcript
	}
	target, err := manager.Kill(ctx, kill.Request{ID: id, Engine: engine, RolloutPath: rolloutPath})
	if err != nil {
		return "", fmt.Errorf("hide %s: %w", id, err)
	}
	return target.ID, nil
}
