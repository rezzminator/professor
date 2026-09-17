package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hostops/pfm/internal/action"
	"hostops/pfm/internal/agentrole"
	pfmchat "hostops/pfm/internal/chat"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/fleetdb"
	"hostops/pfm/internal/headless"
	"hostops/pfm/internal/inject"
	"hostops/pfm/internal/naming"
	"hostops/pfm/internal/rearm"
	"hostops/pfm/internal/spawn"
)

// runSpawnTimings is zero in production, which makes spawn use its live
// defaults. Package integration tests replace it with short timings because
// their local TUI fixtures paint synchronously.
var runSpawnTimings spawn.Timings

// spawnTraceEnv turns on the spawn choreography trace on stderr.
const spawnTraceEnv = "PFM_SPAWN_TRACE"

// runRun starts a chat with the fleet's whole launch ceremony — the
// environment strip, the account's config dir, the cache mode, the autonomy
// flags, its own tmux server on a fleet socket — and then walks away from it.
// No terminal is attached and nothing is eval'd by the caller's shell, so it
// works from a script, a cron job, or another chat's Bash tool.
func runRun(
	args []string,
	stdout, stderr io.Writer,
	runtime commandRuntime,
) int {
	flags := newFlagSet(
		"chat new",
		"usage: pfm chat new --name NAME [--engine cc|cx] [--cwd DIR] "+
			"[--account N] [--1h] [--model M] [--effort E] [--prompt-file PATH] [--role ROLE] "+
			"[--await [--timeout SECS] [--settle SECS] [--progress]] [--attach] [prompt]",
		stderr,
	)
	name := flags.String("name", "", "chat name (a _KILL… name stays out of the list)")
	engine := flags.String(
		"engine",
		"",
		"engine: cc|claude or cx|codex (default: the calling chat's engine, else config)",
	)
	cwd := flags.String("cwd", "", "project directory (default: the current one)")
	account := flags.Int("account", 0, "Claude account (default: the primary one)")
	cache1H := flags.Bool("1h", false, "arm 1h prompt caching")
	model := flags.String("model", "", "model the seat is born with")
	effort := flags.String("effort", "", "reasoning effort the seat is born with")
	promptFile := flags.String("prompt-file", "", "read the launch prompt from a file")
	role := flags.String("role", "", "registered agent role whose constitution the seat reads first, before any prompt")
	await := flags.Bool("await", false, "wait for the first answer and print it (the launch summary moves to stderr)")
	timeout := flags.Int("timeout", askTimeoutSeconds, "with --await: seconds to wait (0 waits forever)")
	settle := flags.Int("settle", askSettleSeconds, "with --await: seconds of quiet before an answer is finished")
	progress := flags.Bool("progress", false, "with --await: print the chat's turns to stderr while waiting")
	attach := flags.Bool("attach", false, "attach this terminal after launch")
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	positional := flags.Args()
	if *name == "" && len(positional) > 0 {
		*name = positional[0]
		positional = positional[1:]
	}
	if *name == "" || *timeout < 0 || *settle < 0 || (*attach && *await) {
		flags.Usage()
		return 2
	}
	resolved := runtime.Paths
	directory, err := runDirectory(*cwd)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
		return 1
	}
	engineName, selectedAccount, err := resolveRunEngineAccount(
		*engine,
		*account,
		runtime.Config,
		fleet.PrimaryAccount(resolved, runtime.Config),
	)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
		return 2
	}
	prompt, err := runPrompt(*promptFile, positional)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
		return 2
	}
	// roleArtifact stays the zero value when --role was not given; the crumb
	// write below is itself gated on *role != "", so a zero Artifact there
	// is never reached.
	var roleArtifact agentrole.Artifact
	if *role != "" {
		constitution, artifact, err := agentrole.Resolve(engineName, *role, directory, resolved.Home)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
			return 2
		}
		roleArtifact = artifact
		prompt = composeRolePrompt(constitution, prompt)
	}
	plan, err := action.HeadlessRun(action.HeadlessRequest{
		Engine:         engineName,
		Name:           *name,
		CWD:            directory,
		Prompt:         prompt,
		Model:          *model,
		Effort:         *effort,
		Home:           resolved.Home,
		PrimaryAccount: selectedAccount,
		Cache1H:        *cache1H,
		Config:         runtime.Config,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
		return 2
	}

	// PFM_SPAWN_TRACE turns on a step-by-step log of the TUI
	// choreography: what was typed, which screen came back, which overlay was
	// dismissed. A chat driven blind is a chat debugged blind.
	var trace io.Writer
	if os.Getenv(spawnTraceEnv) != "" {
		trace = stderr
	}
	titles := runtime.Config.Tmux.Titles
	result, err := spawn.Run(context.Background(), spawn.CommandTmux{
		TmuxDir: resolved.TmuxDir,
		Titles:  &titles,
	}, spawn.Request{
		Trace:               trace,
		Engine:              engineName,
		Name:                *name,
		Socket:              freshEngineSocket(engineName),
		CWD:                 directory,
		Run:                 plan.Run,
		Binary:              plan.Binary,
		Prompt:              prompt,
		PromptOnCommandLine: plan.PromptOnCommandLine,
		Width:               action.HeadlessWidth,
		Height:              action.HeadlessHeight,
		Timings:             runSpawnTimings,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
		return 1
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stderr, "pfm chat new: %s\n", warning)
	}
	// With --await the reply owns stdout, so the launch summary steps aside:
	// `answer=$(pfm chat new --await …)` must be the answer and
	// nothing else.
	summary := stdout
	if *await {
		summary = stderr
	}
	printRunResult(summary, engineName, result)
	if !result.Named {
		return 1
	}
	spawnedAt := time.Now()
	parent := parentChatID()
	state := fleetdb.Open(context.Background(), resolved)
	if parent != "" {
		if err := registerDetachedChild(state, parent, result.Socket, spawnedAt.Unix()); err != nil {
			fmt.Fprintf(
				stderr,
				"pfm chat new: WARNING: chat is live but could not be registered for parent-close cleanup: %v\n",
				err,
			)
		}
	}
	// T1 re-arm: remember this seat's role, and the exact artifact birth
	// read it from, so `pfm chat reload` and chat_self_compact can re-arm it
	// after a reset. Keyed the bare-socket way (see internal/rearm) — a
	// crumb write failure never fails the launch itself, since the seat is
	// already live and born with its constitution either way; it only means
	// this one seat cannot re-arm later.
	if *role != "" {
		if err := os.MkdirAll(resolved.SIDDir, 0o700); err != nil {
			fmt.Fprintf(
				stderr,
				"pfm chat new: WARNING: chat is live but could not remember its role %q for re-arm: %v\n",
				*role,
				err,
			)
		} else if err := rearm.WriteCrumb(resolved.SIDDir, result.Socket, rearm.Crumb{
			Role:         *role,
			ArtifactPath: roleArtifact.Path,
			TOMLKey:      roleArtifact.TOMLKey,
		}); err != nil {
			fmt.Fprintf(
				stderr,
				"pfm chat new: WARNING: chat is live but could not remember its role %q for re-arm: %v\n",
				*role,
				err,
			)
		}
	}
	// result.Socket is the bare session name spawn.Run was asked to create
	// (freshEngineSocket's own output), not a resolvable socket PATH — the
	// inject recorder (internal/inject/engine.go) writes result.SocketPath,
	// the full tmux -S argument. Recording the bare name here left the
	// cosmos graph's receiver-side row lookup (paneKey(socket, pane)) unable
	// to ever match a live row, because no live row's socket is a bare name.
	// ReceiverPane is left unset: spawn.Result carries no pane id (a fresh
	// session's first pane is conventionally "%0", but nothing here confirms
	// that invariant, so it is not invented).
	if err := state.RecordComms(context.Background(), fleetdb.CommsEvent{
		AtNS: spawnedAt.UnixNano(), Kind: fleetdb.KindSpawn, SenderSession: parent,
		Target: *name, ReceiverSocket: filepath.Join(resolved.TmuxDir, result.Socket), Message: prompt,
	}); err != nil {
		fmt.Fprintf(stderr, "pfm: comms ledger: %v\n", err)
	}
	if err := state.Close(); err != nil {
		fmt.Fprintf(stderr, "pfm: comms ledger: close shared state: %v\n", err)
	}
	if prompt == "" {
		return attachRunResult(*attach, result, stdout, stderr)
	}
	var progressOut io.Writer
	if *progress && *await {
		progressOut = stderr
	}
	code := awaitLaunch(
		context.Background(),
		*name,
		*await,
		headless.AwaitOptions{
			Grace:    launchGrace,
			Settle:   time.Duration(*settle) * time.Second,
			Timeout:  time.Duration(*timeout) * time.Second,
			Progress: progressOut,
		},
		result,
		stdout,
		stderr,
		runtime,
	)
	if code != 0 {
		return code
	}
	return attachRunResult(*attach, result, stdout, stderr)
}

func resolveRunEngineAccount(
	requestedEngine string,
	requestedAccount int,
	machine pfmconfig.Config,
	primaryClaude int,
) (pfmengine.ID, int, error) {
	engineInput := requestedEngine
	if strings.TrimSpace(engineInput) == "" {
		if caller, ok := callerEngine(os.Getenv); ok {
			return resolveRunEngineIDAccount(caller, requestedAccount, machine, primaryClaude)
		}
		defaultEngine, err := machine.DefaultEngine()
		if err != nil {
			return "", 0, err
		}
		return resolveRunEngineIDAccount(defaultEngine, requestedAccount, machine, primaryClaude)
	}
	id, err := pfmengine.Parse(engineInput)
	if err != nil {
		return "", 0, err
	}
	return resolveRunEngineIDAccount(id, requestedAccount, machine, primaryClaude)
}

func resolveRunEngineIDAccount(
	id pfmengine.ID,
	requestedAccount int,
	machine pfmconfig.Config,
	primaryClaude int,
) (pfmengine.ID, int, error) {
	account := requestedAccount
	switch id {
	case pfmengine.Claude:
		if account == 0 {
			account = primaryClaude
			if _, found := machine.Account(account); !found && len(machine.Accounts) != 0 {
				account = machine.Accounts[0].ID
			}
		}
		if _, found := machine.Account(account); !found {
			return "", 0, fmt.Errorf("requested Claude account %d is not in the configured roster", account)
		}
	case pfmengine.Codex:
		if account == 0 && len(machine.CodexAccounts) != 0 {
			account = machine.CodexAccounts[0].ID
		}
		if _, found := machine.CodexAccountByID(account); !found {
			return "", 0, fmt.Errorf("requested Codex account %d is not in the configured roster", account)
		}
	case pfmengine.Opencode:
		if account == 0 && len(machine.OpencodeAccounts) != 0 {
			account = machine.OpencodeAccounts[0].ID
		}
		if _, found := machine.OpencodeAccountByID(account); !found {
			return "", 0, fmt.Errorf("OpenCode account %d is not in the configured roster", account)
		}
	}
	return id, account, nil
}

func parentChatID() string {
	if id := os.Getenv("CLAUDE_CODE_SESSION_ID"); id != "" {
		return id
	}
	return os.Getenv("CODEX_THREAD_ID")
}

func registerDetachedChild(state *fleetdb.Store, parent, socket string, createdAt int64) error {
	if state.Degraded() != nil {
		return state.Degraded()
	}
	return state.AddChild(
		context.Background(),
		fleetdb.KindNew,
		parent,
		socket,
		createdAt,
	)
}

func attachRunResult(attach bool, result spawn.Result, stdout, stderr io.Writer) int {
	if !attach {
		return 0
	}
	line := "TMUX= tmux -L " + action.Quote(result.Socket) +
		" attach -t " + action.Quote(result.Session)
	if err := dispatchAction(stdout, line); err != nil {
		fmt.Fprintf(stderr, "pfm chat new: attach: %v\n", err)
		return 1
	}
	return 0
}

// launchGrace is how long a chat that was just created is allowed to be
// missing from a fleet scan before the wait calls it gone. Naming, indexing
// and the engine's first write all have to happen first.
//
// launchProofWindow bounds the delivery proof. It is not a wait for the
// ANSWER — only for the engine's own record of having been asked — so it is
// short enough that a script does not hang on it.
//
// Both are variables so a test can drive the refusal path in seconds instead
// of minutes; nothing outside a test changes them.
var (
	launchGrace       = 45 * time.Second
	launchProofWindow = 90 * time.Second
)

// awaitLaunch proves the launch prompt reached the model, and — with
// --await — brings back the answer.
//
// A prompt that was typed is not a prompt that was delivered: the keystrokes
// can go into a startup overlay, a modal, or an engine that dropped the Enter,
// and every one of those looks like success from the sending end. The engine's
// own transcript is the proof, and this refuses to report a delivery it cannot
// find there.
func awaitLaunch(
	ctx context.Context,
	name string,
	await bool,
	options headless.AwaitOptions,
	result spawn.Result,
	stdout, stderr io.Writer,
	runtimes ...commandRuntime,
) int {
	handle := chatHandle(result.Socket, name)
	if await {
		return awaitAnswer(ctx, "run", name, handle, options, false, stdout, stderr, runtimes...)
	}
	proof := options
	proof.StopOnDelivery = true
	proof.Timeout = launchProofWindow
	turn, err := headless.Await(
		ctx,
		func(ctx context.Context) (headless.Chat, bool, error) {
			return pfmchat.Resolve(ctx, handle, io.Discard, firstRuntime(runtimes))
		},
		proof,
	)
	if turn.Delivered {
		return 0
	}
	if rescueLaunchPrompt(ctx, handle, stderr, runtimes...) {
		rescued, _ := headless.Await(
			ctx,
			func(ctx context.Context) (headless.Chat, bool, error) {
				return pfmchat.Resolve(ctx, handle, io.Discard, firstRuntime(runtimes))
			},
			rescueProofOptions(options),
		)
		if rescued.Delivered {
			fmt.Fprintf(
				stderr,
				"pfm chat new: %s left the prompt unsent in the composer; "+
					"dismissed the overlay, pressed Enter, and the model "+
					"recorded it\n",
				name,
			)
			return 0
		}
	}
	fmt.Fprintf(
		stderr,
		"pfm chat new: %s never recorded the prompt — it was typed but "+
			"the model was never asked (a dismiss-and-Enter retry did not "+
			"land it either); attach it and look: tmux -L %s attach -t %s\n",
		name,
		result.Socket,
		result.Session,
	)
	if err != nil && !errors.Is(err, headless.ErrAwaitTimeout) {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
	}
	return codeUndelivered
}

// launchRescueWindow bounds the second proof wait. The prompt is already in
// the composer by this point, so the model answers as soon as the Enter lands
// or never — a short window either way.
var launchRescueWindow = 30 * time.Second

// launchRescueSettle separates the dismiss from the submit. A TUI that eats
// the first Enter eats a second one sent in the same instant.
var launchRescueSettle = 500 * time.Millisecond

func rescueProofOptions(options headless.AwaitOptions) headless.AwaitOptions {
	proof := options
	proof.StopOnDelivery = true
	proof.Timeout = launchRescueWindow
	return proof
}

// rescueLaunchPrompt presses the keys a human presses when a launch prompt is
// sitting typed-but-unsent: Escape to clear the startup overlay that swallowed
// the submit, then Enter. It reports whether the keys were delivered, not
// whether the model answered — the caller re-proves that against the engine's
// own transcript, because a keypress that reached tmux still proves nothing
// about the model having been asked.
func rescueLaunchPrompt(
	ctx context.Context,
	handle string,
	_ io.Writer,
	runtimes ...commandRuntime,
) bool {
	chat, found, err := pfmchat.Resolve(ctx, handle, io.Discard, firstRuntime(runtimes))
	if err != nil || !found || !chat.Live {
		return false
	}
	socketPath, err := chatSocketPath(chat.Socket)
	if err != nil {
		return false
	}
	pane := chatPaneTarget(chat.Pane, chat.Session, chat.Socket)
	tmux := inject.CommandTmux{}
	if err := tmux.SendKey(ctx, socketPath, pane, "Escape"); err != nil {
		return false
	}
	time.Sleep(launchRescueSettle)
	if err := tmux.SendKey(ctx, socketPath, pane, "Enter"); err != nil {
		return false
	}
	return true
}

// runPrompt takes the launch prompt from a file or from the command line,
// never from both: an inline argument caps out around what a shell will carry,
// which is why --prompt-file exists, and silently preferring one over the
// other would make a truncated brief look delivered.
func runPrompt(path string, args []string) (string, error) {
	inline := strings.Join(args, " ")
	if path == "" {
		return inline, nil
	}
	if inline != "" {
		return "", errors.New("--prompt-file and an inline prompt are mutually exclusive")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt file: %w", err)
	}
	prompt := strings.TrimRight(string(content), "\n")
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("prompt file %s is empty", path)
	}
	return prompt, nil
}

// rolePromptSeparator marks where a --role seat's constitution ends and the
// caller's own prompt begins, the same "\n\n---\n\n" section rule
// internal/recovery uses between its own prompt sections.
const rolePromptSeparator = "\n\n---\n\n"

// composeRolePrompt puts the role constitution first, before any goal — the
// seat is the role from birth. --role alone (no caller prompt) is legal and
// launches a seat that has read its constitution and nothing else.
func composeRolePrompt(constitution, prompt string) string {
	if prompt == "" {
		return constitution
	}
	return constitution + rolePromptSeparator + prompt
}

func runDirectory(requested string) (string, error) {
	directory := requested
	if directory == "" {
		current, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("read current directory: %w", err)
		}
		return current, nil
	}
	info, err := os.Stat(directory)
	if err != nil {
		return "", fmt.Errorf("project directory %s: %w", directory, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project directory %s is not a directory", directory)
	}
	return directory, nil
}

func printRunResult(
	stdout io.Writer,
	engineName pfmengine.ID,
	result spawn.Result,
) {
	state := "named"
	if !result.Named {
		state = "UNNAMED"
	}
	listing := "listed"
	if naming.LabelKilled(result.Name) {
		listing = "killed by its " + naming.KillPrefix + " name"
	}
	fmt.Fprintf(
		stdout,
		"%s\t%s\t%s\t%s\t%s\n",
		engineName,
		result.Name,
		result.Socket,
		state,
		listing,
	)
	fmt.Fprintf(
		stdout,
		"attach: tmux -L %s attach -t %s\n",
		result.Socket,
		result.Session,
	)
}
