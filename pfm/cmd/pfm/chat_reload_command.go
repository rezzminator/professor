package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	pfmchat "hostops/pfm/internal/chat"
	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/kill"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/rearm"
	"hostops/pfm/internal/reload"
	"hostops/pfm/internal/resolve"
	"hostops/pfm/internal/store"
	pfmtmux "hostops/pfm/internal/tmux"
)

const (
	toggleOffFlag     = "off"
	reloadNewFlag     = "--new"
	reloadHideFlag    = "--hide"
	reloadThenFlag    = "--then"
	reloadSocketFlag  = "--sock"
	reloadModelFlag   = "--model"
	reloadEffortFlag  = "--effort"
	reloadPaneFlag    = "--pane"
	reloadOneHourFlag = "--1h"
	reloadAccountFlag = "--account"
)

type reloadCommandTmux struct{}

// startReloadWorker launches the detached worker (Detach: true) and releases
// it; a test overrides this var to script the launch.
var startReloadWorker = func(argv []string, opts deps.StartOptions) error {
	process, err := deps.RealRunner{}.Start(context.Background(), argv, opts)
	if err != nil {
		return err
	}
	return process.Release()
}

func (reloadCommandTmux) command(ctx context.Context, socket string, args ...string) *exec.Cmd {
	return pfmtmux.Command(ctx, "", socket, args...)
}

func (tmux reloadCommandTmux) ListPanes(ctx context.Context, socket string) ([]reload.Pane, error) {
	format := strings.Join(
		[]string{"#{pane_id}", "#{pane_dead}", "#{pane_current_path}", "#{pane_tty}", "#{pane_pid}"},
		"\x1f",
	)
	output, err := tmux.command(ctx, socket, "list-panes", "-a", "-F", format).Output()
	if err != nil {
		return nil, fmt.Errorf("list panes: %w", err)
	}
	rows := make([]reload.Pane, 0)
	for _, line := range strings.Split(strings.TrimSuffix(string(output), "\n"), "\n") {
		if line == "" {
			continue
		}
		// Either spelling of the control separator: see internal/tmux/format.go.
		fields := pfmtmux.FormatSplit(line, 5)
		if len(fields) != 5 {
			return nil, fmt.Errorf("tmux returned %d pane fields", len(fields))
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil {
			return nil, fmt.Errorf("parse pane pid %q: %w", fields[4], err)
		}
		rows = append(
			rows,
			reload.Pane{
				ID:          fields[0],
				Dead:        fields[1] == "1",
				CurrentPath: fields[2],
				TTY:         strings.TrimPrefix(fields[3], "/dev/"),
				PID:         pid,
			},
		)
	}
	return rows, nil
}

func (tmux reloadCommandTmux) SetRemain(ctx context.Context, socket, pane string, on bool) error {
	if on {
		return tmux.command(ctx, socket, "set-option", "-p", "-t", pane, "remain-on-exit", "on").Run()
	}
	return tmux.command(ctx, socket, "set-option", "-p", "-t", pane, "-u", "remain-on-exit").Run()
}

func (tmux reloadCommandTmux) PaneInMode(ctx context.Context, socket, pane string) (bool, error) {
	out, err := tmux.command(ctx, socket, "display-message", "-p", "-t", pane, "#{pane_in_mode}").Output()
	return strings.TrimSpace(string(out)) == "1", err
}

func (tmux reloadCommandTmux) CancelMode(ctx context.Context, socket, pane string) error {
	return tmux.command(ctx, socket, "send-keys", "-t", pane, "-X", "cancel").Run()
}

func (tmux reloadCommandTmux) Capture(ctx context.Context, socket, pane string) (string, error) {
	// Reload decisions concern the active TUI only. Including scrollback lets an
	// old composer or selector masquerade as current state.
	out, err := tmux.command(ctx, socket, "capture-pane", "-t", pane, "-p", "-J").Output()
	return string(out), err
}

func (tmux reloadCommandTmux) SendKey(ctx context.Context, socket, pane, key string) error {
	return tmux.command(ctx, socket, "send-keys", "-t", pane, key).Run()
}

func (tmux reloadCommandTmux) SendLiteral(ctx context.Context, socket, pane, text string) error {
	return tmux.command(ctx, socket, "send-keys", "-t", pane, "-l", "--", text).Run()
}

func (tmux reloadCommandTmux) Respawn(ctx context.Context, socket, pane, cwd, command string) error {
	return tmux.command(ctx, socket, "respawn-pane", "-k", "-t", pane, "-c", cwd, command).Run()
}

func (tmux reloadCommandTmux) Display(ctx context.Context, socket, pane, message string) error {
	return tmux.command(ctx, socket, "display-message", "-t", pane, message).Run()
}

func runChatReloadWithRuntime(
	args []string,
	stdout, stderr io.Writer,
	runtime commandRuntime,
	env paths.Env,
) int {
	env = defaultEnv(env)
	if len(args) == 1 && (args[0] == helpFlag || args[0] == "-h") {
		fmt.Fprintln(stdout, reload.Usage)
		return 0
	}
	if err := validateReloadArgs(args); err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: %v\n", err)
		return 2
	}
	resolved := runtime.Paths
	tmux := reloadCommandTmux{}
	callerSock := reloadSocketArgument(args)
	callerPane := reloadPaneArgument(args)
	// Resolve before detaching; the worker has no tmux ancestry to recover.
	socketPath, pane, _, code := reloadTarget(
		context.Background(), callerSock, callerPane, resolved, runtime, tmux, stderr, env,
	)
	if code != 0 {
		return code
	}
	if err := os.MkdirAll(resolved.SIDDir, 0o700); err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: create worker log directory: %v\n", err)
		return 1
	}
	logPath := filepath.Join(
		resolved.SIDDir,
		"reload-"+filepath.Base(socketPath)+".log",
	)
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

func runChatReloadWorkerWithRuntime(
	args []string,
	stdout, stderr io.Writer,
	runtime commandRuntime,
	env paths.Env,
) int {
	env = defaultEnv(env)
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
		case reloadOneHourFlag:
			if index+1 >= len(args) ||
				(args[index+1] != "on" && args[index+1] != toggleOffFlag && args[index+1] != "1" && args[index+1] != "0") {
				fmt.Fprintln(stderr, "pfm chat reload: --1h needs on|off")
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
	tmux := reloadCommandTmux{}
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
	// Re-arm a remembered role in the same flattened steer as --then.
	if roleCrumb, ok, err := rearm.ReadCrumb(resolved.SIDDir, filepath.Base(socketPath), pane); err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: %v\n", err)
		return 1
	} else if ok {
		// This literal tmux channel supports DefaultThresholdBytes without spill.
		pointer := flattenThenLine(rearm.Pointer(roleCrumb, rearm.DefaultThresholdBytes))
		if then == "" {
			then = pointer
		} else {
			then = then + " " + pointer
		}
		fmt.Fprintf(
			stdout,
			"pfm chat reload: role %q remembered — re-arm pointer appended to the reborn chat's follow-up\n",
			roleCrumb.Role,
		)
	}
	engine := reloadEngine(socketPath)
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
	birthAccount, birthCache, err := reloadBirth(resolved, runtime.Config, socketPath, paneState, stderr, env)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: %v\n", err)
		return 2
	}
	if account == "" {
		account = strconv.Itoa(birthAccount)
		fmt.Fprintf(stdout, "pfm chat reload: no account given — keeping the chat's current account %s\n", account)
	}
	acct, _ := strconv.Atoi(account)
	selected, err := validateReloadAccount(runtime.Config, engine, acct)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: %v\n", err)
		return 2
	}
	cache := birthCache
	if cacheOverride != "" {
		cache = cacheOverride == "on" || cacheOverride == "1"
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
			Engine:      engine,
			SocketPath:  socketPath,
			Pane:        pane,
			PanePID:     paneState.PID,
			SessionID:   id,
			Transcript:  transcript,
			CWD:         cwd,
			Account:     acct,
			AccountIDs:  selected.IDs,
			CodexHome:   selected.CodexHome,
			CodexBinary: selected.CodexBinary,
			CodexYolo:   selected.CodexYolo,
			Cache1H:     cache,
			Then:        then,
			Name:        name,
			Model:       model,
			Effort:      effort,
			Home:        resolved.Home,
			Machine:     runtime.Config,
		},
		options,
		tmux,
		reloadProc{procfs: gather.NewProcFS(resolved.ProcRoot)},
		stderr,
	)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: %v\n", err)
		return 1
	}
	if result.New {
		if newSeat {
			fmt.Fprintf(
				stdout,
				"pfm chat reload: rebooted FRESH as requested: %s %s\n",
				filepath.Base(socketPath),
				pane,
			)
		} else {
			fmt.Fprintln(stdout, "pfm chat reload: no transcript yet — rebooted FRESH")
		}
	} else {
		fmt.Fprintf(stdout, "pfm chat reload: respawned in place: %s %s\n", filepath.Base(socketPath), pane)
	}
	if newSeat && hide {
		// Only now: the old chat has /exited and the reborn one owns the
		// pane. A reload that failed returned above, so a live chat is never
		// hidden by the command that failed to replace it.
		if leftBehind == "" {
			fmt.Fprintln(
				stdout,
				"pfm chat reload: --hide — nothing to hide, the conversation left behind had no transcript yet",
			)
			return 0
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

func validateReloadArgs(args []string) error {
	account := false
	newSeat := false
	hide := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case reloadNewFlag:
			if newSeat {
				return errors.New("new specified twice")
			}
			newSeat = true
		case reloadHideFlag:
			if hide {
				return errors.New("hide specified twice")
			}
			hide = true
		case reloadThenFlag, reloadSocketFlag, reloadPaneFlag, reloadModelFlag, reloadEffortFlag:
			// --pane is worker-only plumbing (see reloadTarget): accepted here
			// because this same validator runs on the worker's expanded argv,
			// but it is deliberately absent from reload.Usage and
			// reloadArgumentHint — no caller-facing doc ever tells a human or
			// a model to pass it.
			if index+1 >= len(args) {
				return fmt.Errorf("%s needs a value", args[index])
			}
			index++
		case reloadAccountFlag:
			if index+1 >= len(args) {
				return errors.New("--account needs an account number, as in --account 2")
			}
			if _, valid := positiveAccount(args[index+1]); !valid {
				return fmt.Errorf(
					"--account takes an account NUMBER, not %q — see `pfm config show` for the configured accounts",
					args[index+1],
				)
			}
			if account {
				return errors.New("account specified twice")
			}
			account = true
			index++
		case reloadOneHourFlag:
			if index+1 >= len(args) ||
				(args[index+1] != "on" && args[index+1] != toggleOffFlag && args[index+1] != "1" && args[index+1] != "0") {
				return errors.New("--1h needs on|off")
			}
			index++
		default:
			if _, valid := positiveAccount(args[index]); !valid {
				return errors.New(reloadArgumentHint(args[index]))
			}
			if account {
				return errors.New("account specified twice")
			}
			account = true
		}
	}
	if hide && !newSeat {
		return errors.New("--hide needs --new — a reload that resumes the same conversation cannot hide it")
	}
	return nil
}

// flattenThenLine collapses embedded newlines to spaces. deliverThen types
// request.Then into the reborn pane with a single literal tmux send-keys -l
// call (reload.go); a raw newline byte in that stream lands in the pane
// exactly like an Enter keypress, submitting the composer mid-prompt. Both
// an operator's own --then value and the T1 role re-arm pointer this file
// appends to it go through this same flattening, so the two can never
// diverge on what "one line" means to this delivery channel.
func flattenThenLine(text string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(text)
}

// reloadArgumentHint turns a rejected word into an error the CALLER can act on
// without re-reading the usage line and guessing again.
//
// The usage string alone was not enough: a caller told "reload the cache off"
// sent `reload cache off`, got the bare usage back, and had to work out on its
// own that "cache" meant --1h. An error that only restates the grammar makes
// the reader do the mapping the command already knows how to do.
func reloadArgumentHint(argument string) string {
	suggestion := ""
	switch strings.ToLower(strings.TrimPrefix(argument, "--")) {
	case "cache", "1h", "ttl", "prompt-cache":
		suggestion = "did you mean --1h on|off?"
	case "account", "acct", "seat", "profile":
		suggestion = "did you mean --account N?"
	case "fresh", newAction, "restart", "reset":
		suggestion = "did you mean --new?"
	case "hide", "kill", "close", "forget":
		suggestion = "did you mean --hide? (beside --new: hides the conversation left behind)"
	case thenAction, "prompt", "continue":
		suggestion = "did you mean --then \"prompt\"?"
	case "sock", "socket", chatCommand, "target":
		suggestion = "did you mean --sock socket? (omit it and the calling chat is detected automatically)"
	case "model":
		suggestion = "did you mean --model NAME?"
	case "effort", "level", "reasoning", "thinking":
		suggestion = "did you mean --effort LEVEL?"
	}
	if suggestion == "" {
		suggestion = "an account is passed as --account N, and every other setting has its own flag"
	}
	return fmt.Sprintf("%q is not a reload argument — %s\n%s", argument, suggestion, reload.Usage)
}

func positiveAccount(value string) (int, bool) {
	account, err := strconv.Atoi(value)
	return account, err == nil && account > 0
}

func reloadRequestedAccount(args []string) int {
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case reloadNewFlag, reloadHideFlag:
			continue
		case reloadThenFlag, reloadSocketFlag, reloadPaneFlag, reloadOneHourFlag, reloadModelFlag, reloadEffortFlag:
			index++
			continue
		case reloadAccountFlag:
			if index+1 < len(args) {
				if account, valid := positiveAccount(args[index+1]); valid {
					return account
				}
			}
			index++
			continue
		}
		// The bare positional stays accepted for callers already using it;
		// only the documented spelling changed.
		if account, valid := positiveAccount(args[index]); valid {
			return account
		}
	}
	return 0
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

// reloadProc adapts the gathered process table to the tree walker. It holds ONE
// reader rather than building a fresh one per call: on macOS the reader carries
// a snapshot cache, and a per-call instance would resample the world each hop.
type reloadProc struct{ procfs gather.ProcFS }

func (proc reloadProc) PIDs() ([]int, error) { return proc.procfs.PIDs() }
func (proc reloadProc) Cmdline(pid int) ([]string, error) {
	return proc.procfs.Cmdline(pid)
}

func (proc reloadProc) Environ(pid int) (map[string]string, error) {
	return proc.procfs.Environ(pid)
}

func (proc reloadProc) Stat(pid int) (gather.ProcStat, error) {
	return proc.procfs.Stat(pid)
}

func reloadBirth(
	resolved paths.Values,
	machine pfmconfig.Config,
	socketPath string,
	pane reload.Pane,
	stderr io.Writer,
	env paths.Env,
) (int, bool, error) {
	env = defaultEnv(env)
	engine := reloadEngine(socketPath)
	if engine == pfmengine.OpenCode {
		return 0, false, errors.New("OpenCode does not support in-place reload")
	}
	ids := machine.AccountIDs()
	if engine == pfmengine.Codex {
		ids = machine.CodexAccountIDs()
	}
	if len(ids) == 0 {
		return 0, false, fmt.Errorf("no %s accounts configured", reloadEngineLabel(engine))
	}
	account, cache := ids[0], true
	proc := gather.NewProcFS(resolved.ProcRoot)
	procTree := reloadProc{procfs: proc}
	matcher, err := gather.MatcherFor(engine)
	if err != nil {
		return 0, false, err
	}
	binary := machine.Claude.Binary
	if engine == pfmengine.Codex {
		binary = machine.Codex.Binary
	}
	pids, err := proc.PIDs()
	if err != nil {
		fmt.Fprintf(
			stderr,
			"pfm chat reload: inspect birth processes for %s: %v; using safe defaults\n",
			filepath.Base(socketPath),
			err,
		)
		return account, cache, nil
	}
	for _, pid := range pids {
		argv, err := proc.Cmdline(pid)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat reload: inspect process %d command: %v\n", pid, err)
			continue
		}
		if !matcher.IsCommand(argv, binary) {
			continue
		}
		inPane, err := reloadProcessInPane(procTree, pid, pane.PID)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat reload: inspect process %d ancestry: %v\n", pid, err)
			continue
		}
		if !inPane {
			continue
		}
		env, err := proc.Environ(pid)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat reload: inspect process %d environment: %v\n", pid, err)
			continue
		}
		if engine == pfmengine.Codex {
			account = accountForCodexHome(machine, env["CODEX_HOME"])
		} else {
			account = machine.AccountForConfigDir(env["CLAUDE_CONFIG_DIR"])
			cache = env["FORCE_PROMPT_CACHING_5M"] != "1"
		}
		return account, cache, nil
	}
	// A tool shell can be detached from the seat's process tree. In that case
	// its own birth config is the only safe account rung for a cache-only reload.
	if engine == pfmengine.Codex {
		account = accountForCodexHome(machine, env.Get("CODEX_HOME"))
	} else {
		account = machine.AccountForConfigDir(env.Get("CLAUDE_CONFIG_DIR"))
	}
	return account, cache, nil
}

func reloadEngine(socketPath string) pfmengine.ID {
	id, _ := pfmengine.FromSocket(filepath.Base(socketPath))
	return id
}

func reloadEngineLabel(id pfmengine.ID) string {
	if id == "" {
		return "unknown-engine"
	}
	return pfmengine.MustLookup(id).Short
}

func accountForCodexHome(machine pfmconfig.Config, home string) int {
	if len(machine.CodexAccounts) == 0 {
		return 0
	}
	cleaned := filepath.Clean(home)
	for _, account := range machine.CodexAccounts {
		if cleaned == filepath.Clean(account.Home) {
			return account.ID
		}
	}
	return machine.CodexAccounts[0].ID
}

// reloadAccountSelection is the roster verdict a reload needs BEYOND the
// machine config it already carries: the Claude half is now the door's job, so
// only the roster and the Codex seat's own fields survive here.
type reloadAccountSelection struct {
	IDs         []int
	CodexHome   string
	CodexBinary string
	CodexYolo   bool
}

func validateReloadAccount(machine pfmconfig.Config, engine pfmengine.ID, account int) (reloadAccountSelection, error) {
	switch engine {
	case pfmengine.OpenCode:
		return reloadAccountSelection{}, errors.New("OpenCode does not support in-place reload")
	case pfmengine.Codex:
		if len(machine.CodexAccounts) == 0 {
			return reloadAccountSelection{}, errors.New("no Codex accounts configured")
		}
		selected, found := machine.CodexAccountByID(account)
		if !found {
			return reloadAccountSelection{}, fmt.Errorf(
				"requested Codex account %d is not in the configured roster",
				account,
			)
		}
		policy := machine.EffectiveCodex(account)
		return reloadAccountSelection{
			IDs: machine.CodexAccountIDs(), CodexHome: selected.Home,
			CodexBinary: policy.Binary, CodexYolo: policy.Yolo,
		}, nil
	case pfmengine.Claude:
		// Continue below: Claude has the legacy account/config-dir policy.
	default:
		return reloadAccountSelection{}, fmt.Errorf("unknown reload engine %q", engine)
	}
	if len(machine.Accounts) == 0 {
		return reloadAccountSelection{}, errors.New("no Claude accounts configured")
	}
	if _, found := machine.Account(account); !found {
		return reloadAccountSelection{}, fmt.Errorf(
			"requested Claude account %d is not in the configured roster",
			account,
		)
	}
	return reloadAccountSelection{IDs: machine.AccountIDs()}, nil
}

func reloadProcessInPane(proc reloadProc, pid, panePID int) (bool, error) {
	current := pid
	for depth := 0; depth <= 4; depth++ {
		if current == panePID {
			return true, nil
		}
		stat, err := proc.Stat(current)
		if err != nil {
			return false, err
		}
		if stat.ParentPID <= 1 || stat.ParentPID == current {
			break
		}
		current = stat.ParentPID
	}
	return false, nil
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
	engine := reloadEngine(socketPath)
	if id == "" && allowAmbient {
		ambient := env.Get("CLAUDE_CODE_SESSION_ID")
		if engine == pfmengine.Codex {
			ambient = env.Get("CODEX_THREAD_ID")
		}
		if fleet.ChatIDPattern.MatchString(ambient) {
			path, err := findEngineTranscript(resolved, machine, engine, ambient)
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
		path, err := findEngineTranscript(resolved, machine, engine, id)
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

func findEngineTranscript(
	resolved paths.Values,
	machine pfmconfig.Config,
	engine pfmengine.ID,
	id string,
) (string, error) {
	switch engine {
	case pfmengine.OpenCode:
		return "", errors.New("OpenCode does not support in-place reload")
	case pfmengine.Claude:
		return findClaudeTranscript(resolved.Roots[pfmengine.Claude], id)
	case pfmengine.Codex:
		// Continue below: Codex searches every configured rollout home.
	default:
		return "", fmt.Errorf("unknown reload engine %q", engine)
	}
	for _, account := range machine.CodexAccounts {
		found := ""
		err := filepath.WalkDir(
			filepath.Join(account.Home, "sessions"),
			func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry != nil && !entry.IsDir() && strings.Contains(filepath.Base(path), id) &&
					filepath.Ext(path) == ".jsonl" {
					found = path
					return filepath.SkipAll
				}
				return nil
			},
		)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("search Codex rollouts under %q: %w", account.Home, err)
		}
		if found != "" {
			return found, nil
		}
	}
	return "", nil
}

func findClaudeTranscript(roots []string, id string) (string, error) {
	for _, root := range roots {
		found := ""
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry != nil && !entry.IsDir() && filepath.Base(path) == id+".jsonl" {
				found = path
				return filepath.SkipAll
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("search Claude transcripts under %q: %w", root, err)
		}
		if found != "" {
			return found, nil
		}
	}
	return "", nil
}
