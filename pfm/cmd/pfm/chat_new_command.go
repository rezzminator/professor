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

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/agentrole"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/inject"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/workbench"
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
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	runtime commandRuntime,
	env paths.Env,
	clk clock.Clock,
) int {
	env = defaultEnv(env)
	clk = defaultClock(clk)
	flags := cli.NewFlagSet(
		"chat new",
		"usage: pfm chat new [--name NAME] [--engine cc|cx] [--cwd DIR] "+
			"[--account N] [--cache 1h|5m] [--model M] [--effort E] [--prompt-file PATH] [--agent-role ROLE] "+
			"[--harness-prompt PATH] [--await [--timeout SECS] [--settle SECS] [--progress]] [--attach] [prompt]",
		stderr,
	)
	name := flags.String(
		"name",
		"",
		"chat name (optional inside a workbench: {name}:{n}; a _KILL… name stays out of the list)",
	)
	engine := flags.String(
		"engine",
		"",
		"engine: cc|claude or cx|codex (default: the calling chat's engine, else config)",
	)
	cwd := flags.String("cwd", "", "project directory (default: the current one)")
	account := flags.Int("account", 0, "Claude account (default: the primary one)")
	cache := flags.String("cache", "", "prompt cache for this launch: 1h or 5m")
	model := flags.String("model", "", "model the seat is born with")
	effort := flags.String("effort", "", "reasoning effort the seat is born with")
	promptFile := flags.String("prompt-file", "", "read the launch prompt from a file")
	role := flags.String("agent-role", "", "registered agent role carried by the seat's prompt channel")
	harnessPrompt := flags.String("harness-prompt", "", "claude only: system prompt file replacing the staged one")
	await := flags.Bool("await", false, "wait for the first answer and print it (the launch summary moves to stderr)")
	timeout := flags.Int("timeout", askTimeoutSeconds, "with --await: seconds to wait (0 waits forever)")
	settle := flags.Int("settle", askSettleSeconds, "with --await: seconds of quiet before an answer is finished")
	progress := flags.Bool("progress", false, "with --await: print the chat's turns to stderr while waiting")
	attach := flags.Bool("attach", false, "attach this terminal after launch")
	positional, parseCode, ok := cli.ParseFlagsAroundName(flags, name, args)
	if !ok {
		return parseCode
	}
	if *timeout < 0 || *settle < 0 || (*attach && *await) {
		flags.Usage()
		return 2
	}
	if *cache != "" && *cache != "1h" && *cache != "5m" {
		fmt.Fprintln(stderr, "pfm chat new: --cache must be 1h or 5m")
		return 2
	}
	var cache1H *bool
	if *cache != "" {
		choice := *cache == "1h"
		cache1H = &choice
	}
	resolved := runtime.Paths
	directory, err := runDir(*cwd)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
		return 1
	}
	if *name == "" {
		auto, found, nameErr := pfmchat.ReserveWorkbenchName(ctx, directory, stderr, &runtime)
		if nameErr != nil {
			fmt.Fprintf(stderr, "pfm chat new: %v\n", nameErr)
			return 1
		}
		if !found {
			flags.Usage()
			return 2
		}
		*name = auto.Name
		ctx = pfmchat.WithWorkbenchNameReservation(ctx, auto)
	}
	defer pfmchat.ReleaseUnusedWorkbenchName(ctx, *name, stderr)
	requestedEngine, _ := pfmengine.Parse(*engine)
	if *role != "" && requestedEngine == pfmengine.OpenCode {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", agentrole.ValidateSeatPromptPolicy(requestedEngine, ""))
		return 2
	}
	fleetPrimary, err := fleet.PrimaryAccount(resolved, runtime.Config)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat new: read primary account: %v\n", err)
		return 1
	}
	engineName, selectedAccount, err := resolveRunEngineAccount(*engine, *account, runtime.Config, fleetPrimary, env)
	if *engine == "" {
		bench, found, lookupErr := workbench.Nearest(directory)
		if lookupErr != nil {
			fmt.Fprintf(stderr, "pfm chat new: %v\n", lookupErr)
			return 2
		}
		if found && bench.Err == nil {
			// An engine with an account but no headless planner is passed over,
			// and taken only when nothing else is left, so its own refusal names it.
			var unplanned pfmengine.ID
			unplannedAccount := 0
			picked, ok := workbench.PickEngine(bench, engineName, func(id pfmengine.ID) bool {
				_, candidate, accountErr := resolveRunEngineIDAccount(id, *account, runtime.Config, fleetPrimary)
				if accountErr != nil {
					return false
				}
				if _, plannerErr := action.PlannerFor(id); plannerErr != nil {
					if unplanned == "" {
						unplanned, unplannedAccount = id, candidate
					}
					return false
				}
				selectedAccount = candidate
				return true
			})
			if !ok && unplanned != "" {
				picked, ok, selectedAccount = unplanned, true, unplannedAccount
			}
			if !ok {
				words := make([]string, len(bench.Engines))
				for i, id := range bench.Engines {
					words[i] = pfmengine.MustLookup(id).LongName
				}
				fmt.Fprintf(
					stderr,
					"pfm chat new: workbench %s enables %s, and this machine has no account for any of them\n",
					bench.Dir,
					strings.Join(words, ", "),
				)
				return 2
			}
			engineName, err = picked, nil
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
		return 2
	}
	persona, err := workbench.ForLaunch(directory, engineName, workbench.New)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
		return 2
	}
	harnessPath, harnessBody, err := agentrole.LoadHarnessPromptFor(engineName, *harnessPrompt)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
		return 2
	}
	socket := spawn.FreshSocket(engineName)
	if *role != "" && harnessPath == "" && !persona.Applies() {
		policy := runtime.Config.EffectiveClaude(selectedAccount).SystemPrompt
		if policyErr := agentrole.ValidateSeatPromptPolicy(engineName, policy); policyErr != nil {
			fmt.Fprintf(stderr, "pfm chat new: %v\n", policyErr)
			return 2
		}
	}
	prompt, err := runPrompt(*promptFile, positional)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
		return 2
	}
	promptChannel := harnessPath
	if harnessPath == "" && persona.Applies() {
		promptChannel = persona.Body
		if engineName == pfmengine.Claude {
			promptChannel = persona.Prompt
		}
	}
	seatStateWritten := false
	defer func() {
		if seatStateWritten {
			if removeErr := agentrole.RemoveSeatPrompt(resolved.SIDDir, socket, ""); removeErr != nil {
				fmt.Fprintf(stderr, "pfm chat new: clean up unused seat prompt state: %v\n", removeErr)
			}
		}
	}()
	if *role != "" {
		seatPrompt, constitution, composeErr := agentrole.ResolveSeatPrompt(
			engineName, *role, directory, resolved.Home, harnessBody,
		)
		if composeErr != nil {
			fmt.Fprintf(stderr, "pfm chat new: %v\n", composeErr)
			return 2
		}
		seatPromptPath, pathErr := agentrole.SeatPromptPath(resolved.SIDDir, socket, "")
		if pathErr != nil {
			fmt.Fprintf(stderr, "pfm chat new: %v\n", pathErr)
			return 2
		}
		if writeErr := agentrole.WriteSeatPrompt(resolved.SIDDir, socket, "", seatPrompt); writeErr != nil {
			fmt.Fprintf(stderr, "pfm chat new: %v\n", writeErr)
			return 2
		}
		seatStateWritten = true
		if engineName == pfmengine.Claude {
			promptChannel = seatPromptPath
		} else {
			promptChannel = constitution
		}
	}
	seatStateWritten = seatStateWritten || harnessPath != ""
	if err := agentrole.WriteHarnessPromptRecord(resolved.SIDDir, socket, "", harnessPath); err != nil {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
		return 2
	}
	if engineName == pfmengine.Codex && persona.Applies() {
		if err := workbench.EnsureMirror(persona.Bench, engineName, resolved.Home); err != nil {
			fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
			return 2
		}
	}
	plan, err := action.HeadlessRun(action.HeadlessRequest{
		Engine:         engineName,
		Name:           *name,
		CWD:            directory,
		Prompt:         prompt,
		PromptChannel:  promptChannel,
		Model:          persona.ModelOr(*model),
		Effort:         persona.EffortOr(*effort),
		Home:           resolved.Home,
		PrimaryAccount: selectedAccount,
		Cache1H:        cache1H,
		Config:         runtime.Config,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat new: %v\n", err)
		return 2
	}
	if plan.Record != nil {
		if err := fleetdb.RecordLaunch(ctx, resolved, *plan.Record, clk.Now().Unix()); err != nil {
			fmt.Fprintf(stderr, "pfm: record launch %s: %v\n", plan.Record.SessionID, err)
		}
	}

	// PFM_SPAWN_TRACE turns on a step-by-step log of the TUI
	// choreography: what was typed, which screen came back, which overlay was
	// dismissed. A chat driven blind is a chat debugged blind.
	var trace io.Writer
	if env.Get(spawnTraceEnv) != "" {
		trace = stderr
	}
	titles := runtime.Config.Tmux.Titles
	result, err := spawn.Run(ctx, spawn.TmuxSpawner{
		TmuxDir: resolved.TmuxDir,
		Titles:  &titles,
	}, spawn.Request{
		Trace:  trace,
		Engine: engineName,
		Name:   *name,
		Socket: socket,
		CWD:    directory,
		Run:    plan.Run,
		Binary: plan.Binary, CodexHome: plan.CodexHome,
		Prompt:              prompt,
		PromptOnCommandLine: plan.PromptOnCommandLine,
		Width:               action.HeadlessWidth,
		Height:              action.HeadlessHeight,
		Timings:             runSpawnTimings,
	})
	if err = pfmchat.CommitWorkbenchLaunch(ctx, result.Socket != "", *name, err); err != nil {
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
		pfmchat.RecordVerb(context.Background(), "new", 1)
		if result.TrustHeld {
			seatStateWritten = false // the live chat keeps its seat prompt state
			fmt.Fprintf(stderr, "pfm chat new: %s\n", result.TrustRefusal(*name, directory))
			return codeUndelivered
		}
		return 1
	}
	seatStateWritten = false
	pfmchat.RecordVerb(context.Background(), "new", 0)
	spawnedAt := clk.Now()
	parent := parentChatID(ctx, env)
	state := fleetdb.OpenSharedState(context.Background(), resolved)
	if parent != "" {
		if err := registerDetachedChild(state, parent, result.Socket, spawnedAt.Unix()); err != nil {
			fmt.Fprintf(
				stderr,
				"pfm chat new: WARNING: chat is live but could not be registered for parent-close cleanup: %v\n",
				err,
			)
		}
	}
	// result.Socket is the bare session name spawn.Run was asked to create
	// (spawn.FreshSocket's own output), not a resolvable socket PATH — the
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
		ctx,
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
		clk,
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
	envs ...paths.Env,
) (pfmengine.ID, int, error) {
	env := firstEnv(envs)
	engineInput := requestedEngine
	if strings.TrimSpace(engineInput) == "" {
		if caller, ok := callerEngine(env.Get); ok {
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
	case pfmengine.OpenCode:
		if account == 0 && len(machine.OpenCodeAccounts) != 0 {
			account = machine.OpenCodeAccounts[0].ID
		}
		if _, found := machine.OpenCodeAccountByID(account); !found {
			return "", 0, fmt.Errorf("OpenCode account %d is not in the configured roster", account)
		}
	}
	return id, account, nil
}

func parentChatID(ctx context.Context, env paths.Env) string {
	if self, ok := pfmchat.ScopedSelf(ctx); ok {
		return self.ID
	}
	if id := env.Get("CLAUDE_CODE_SESSION_ID"); id != "" {
		return id
	}
	return env.Get("CODEX_THREAD_ID")
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
	if err := action.Dispatch(stdout, line); err != nil {
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
	clk clock.Clock,
	runtimes ...commandRuntime,
) int {
	handle := chatHandle(result.Socket, name)
	if await {
		return awaitAnswer(ctx, "run", name, handle, options, false, stdout, stderr, runtimes...)
	}
	turn, err := headless.Await(
		ctx,
		chatResolver(handle, runtimes...),
		deliveryProofOptions(options, launchProofWindow),
	)
	if turn.Delivered {
		return 0
	}
	outcome := retryLaunchPrompt(ctx, handle, stderr, clk, runtimes...)
	if outcome == inject.RescueTrustHeld {
		fmt.Fprintf(stderr, "pfm chat new: %s\n", result.TrustRefusal(name, ""))
		return codeUndelivered
	}
	if outcome == inject.RescueKeysPressed {
		rescued, _ := headless.Await(
			ctx,
			chatResolver(handle, runtimes...),
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
	return deliveryProofOptions(options, launchRescueWindow)
}

func deliveryProofOptions(options headless.AwaitOptions, timeout time.Duration) headless.AwaitOptions {
	proof := options
	proof.StopOnDelivery = true
	proof.Timeout = timeout
	return proof
}

// retryLaunchPrompt retries a launch prompt sitting typed-but-unsent
// (inject.RescueLaunchPrompt) and reports what it did, not whether the model
// answered — the caller re-proves that against the engine's own transcript.
func retryLaunchPrompt(
	ctx context.Context,
	handle string,
	stderr io.Writer,
	clk clock.Clock,
	runtimes ...commandRuntime,
) inject.RescueOutcome {
	chat, found, err := pfmchat.Resolve(ctx, handle, io.Discard, firstRuntime(runtimes))
	if err != nil || !found || !chat.Live {
		return inject.RescueNotSent
	}
	socketPath, err := chatSocketPath(chat.Socket)
	if err != nil {
		return inject.RescueNotSent
	}
	pane := chatPaneTarget(chat.Pane, chat.Session, chat.Socket)
	outcome, err := inject.RescueLaunchPrompt(ctx, inject.TmuxInjector{}, socketPath, pane, clk, launchRescueSettle)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat new: %s: dismiss-and-Enter retry: %v\n", handle, err)
	}
	return outcome
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

func runDir(requested string) (string, error) {
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
