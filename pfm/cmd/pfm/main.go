// Command pfm manages live and resumable Claude and Codex chats.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	callmetercmd "github.com/rezzminator/professor/pfm/internal/callmeter/command"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/doctor"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/harvestcli"
	"github.com/rezzminator/professor/pfm/internal/hookentry"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/picker"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/stale"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/update"
)

const (
	chatCommand       = "chat"
	initCommand       = "init"
	indexCommand      = "index"
	headlessCommand   = "headless"
	whoamiCommand     = "whoami"
	versionCommand    = "version"
	configCommand     = "config"
	archiveCommand    = "archive"
	internalCommand   = "internal"
	reloadRunCommand  = "reload-run"
	serveCommand      = "serve"
	installCommand    = "install"
	mcpCommand        = "mcp"
	updateCommand     = "update"
	doctorCommand     = "doctor"
	checkAction       = "check"
	statuslineCommand = "statusline"
	callmeterCommand  = "callmeter"
)

var version = config.DevelopmentVersion

// topLevelSubcommands lists every argv[0] case for reachability and installer parity.
var topLevelSubcommands = []string{
	versionCommand, "ls", chatCommand, "harvest", headlessCommand, indexCommand, doctorCommand,
	configCommand, "reap", archiveCommand, "heal", "name-sync", statuslineCommand,
	pfmengine.MustLookup(pfmengine.OpenCode).LongName,
	"usage-hook", installCommand, "uninstall", updateCommand, initCommand, whoamiCommand,
	"issues", mcpCommand, pfmengine.MustLookup(pfmengine.Codex).LongName, internalCommand, "log", callmeterCommand,
}

// internalSubcommands names each runInternal branch for usage and installer parity.
var internalSubcommands = []string{
	"agent-open", callmeterCommand, "chat-server", "claude-launch", "claude-version", "clear-kill",
	"codex-launch", "compact-nudge", "epic-inject",
	"exit-close", "exit-intercept", "explore-deny", "git-guard", "kill-exit", "launch",
	"launcher-repair", "orchestrator-wait", "primary-get", "primary-set", "reload-intercept", "rr-dir",
	reloadRunCommand, "stale", statuslineCommand, thenAction, "tmux-title-renudge", "update-check",
}

func main() {
	// installer cannot import cmd/pfm (main package); this package-level
	// registry is the seam that tells it which pfm-shaped hook subcommands
	// THIS binary implements, so removeRetiredHookCommands only ever strips
	// a hook naming a subcommand no version of this binary's dispatch would
	// recognize (issue #24 F1) — never an operator's own `pfm doctor` or
	// `pfm internal claude-version` hook.
	installer.SetImplementedSubcommands(topLevelSubcommands, internalSubcommands)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) (exitCode int) {
	configPath, args, err := splitGlobalConfig(args)
	if err != nil {
		fmt.Fprintf(stderr, "pfm: %v\n", err)
		printUsage(stderr)
		return 2
	}
	runtime, err := config.LoadRuntime(configPath)
	if err != nil {
		if !diagnosticCommand(args) {
			fmt.Fprintf(stderr, "pfm: config: %v\n", err)
			return 1
		}
		runtime, err = config.LoadDiagnosticRuntime(configPath)
		if err != nil {
			fmt.Fprintf(stderr, "pfm: config: %v\n", err)
			return 1
		}
	}
	runtime.Version = version
	finishLog := openActivityLog(args, runtime, stderr)
	defer func() { finishLog(exitCode) }()
	// The one place the machine config reaches a Codex rename's proof.
	spawn.UseCodexHomes(runtime.Config.CodexHomes())
	if len(args) == 0 {
		return picker.Run(nil, stdout, stderr, runtime)
	}

	switch args[0] {
	case "version", "--version":
		return runVersion(args[1:], stdout, stderr)
	case "ls":
		return picker.Run(args[1:], stdout, stderr, runtime)
	case "chat":
		return runChatWithRuntime(args[1:], os.Stdin, stdout, stderr, runtime, context.Background())
	case "harvest":
		return harvestcli.Harvest(args[1:], stdout, stderr, runtime)
	case "headless":
		return runHeadless(args[1:], stdout, stderr, runtime)
	case "index":
		return runIndex(args[1:], stdout, stderr, runtime, clock.Real)
	case "log":
		return runLog(args[1:], stdout, stderr, runtime)
	case "callmeter":
		return callmetercmd.CLI(args[1:], stdout, stderr, runtime)
	case "doctor":
		return doctor.Run(
			args[1:],
			stdout,
			stderr,
			runtime,
			doctor.Dependencies{ExpectedEngineCapabilities: expectedEngineCapabilities},
		)
	case "config":
		return runConfig(args[1:], stdout, stderr, runtime)
	case "reap":
		return runReap(args[1:], stdout, stderr, runtime)
	case "archive":
		return runArchive(args[1:], stdout, stderr, runtime)
	case "heal":
		return runHeal(args[1:], stdout, stderr)
	case "name-sync":
		return runNameSync(args[1:], stdout, stderr, runtime)
	case "statusline":
		return runStatuslineWithRuntime(args[1:], os.Stdin, stdout, stderr, runtime, paths.OSEnv{})
	case "usage-hook":
		return runUsageHookWithRuntime(args[1:], stdout, stderr, runtime, paths.OSEnv{})
	case "install":
		return runInstall(args[1:], stdout, stderr, runtime)
	case "uninstall":
		return runUninstall(args[1:], stdout, stderr, runtime)
	case "update":
		return update.Run(args[1:], stdout, stderr, runtime)
	case "init":
		return runInit(args[1:], stdout, stderr, runtime)
	case "whoami":
		return runWhoami(args[1:], stdout, stderr, runtime)
	case "issues":
		return runIssues(args[1:], stdout, stderr, runtime)
	case "mcp":
		return runMCP(args[1:], stdout, stderr, runtime)
	case pfmengine.MustLookup(pfmengine.Codex).LongName:
		return runCodex(args[1:], stdout, stderr, runtime)
	case pfmengine.MustLookup(pfmengine.OpenCode).LongName:
		return runOpenCode(args[1:], stdout, stderr, runtime)
	case "internal":
		return runInternal(args[1:], stdout, stderr, runtime)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "pfm: unknown command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	for _, line := range []string{
		"usage: pfm [--config PATH] <command> [options]", "", "operator commands:",
		"  ls        list or pick fleet chats",
		"  chat      operate on one chat: new, open, inject, ask, read, stream, name, kill, end",
		"  headless  run Claude or OpenCode through one isolated process interface",
		"  harvest   fetch and convert URL, DOI, ISBN, PMID, PMCID, or local path; download files",
		"  index     refresh the transcript index",
		"  whoami    print this chat's own tmux session name",
		"  issues    list servicedesk complaints filed through issue_servicedesk",
		"  reap      classify the socket graveyard; --apply reclaims it",
		"  archive   move killed chats and old subagent transcripts out of sight, reversibly",
		"  heal      report or repair wedged Codex history projections",
		"  install   wire or remove the self-contained host integration",
		"  uninstall remove the self-contained host integration",
		"  update    update the binary; check, adopt, pin, ignore, or drop project template baselines",
		"  init      scaffold project templates once and pin their baselines",
		"  config    initialize, inspect, or validate machine configuration",
		"  doctor    inspect fleet database and jail health",
		"  log       read this home's activity log: --since --level --chat --cmd --follow",
		"  callmeter report which files, commands and calls filled agent contexts",
		"  version   print the pfm version", "", "wiring commands:",
		"  name-sync converge live chat window names",
		"  statusline render the native Claude status line",
		"  usage-hook the fail-open usage-limit prompt hook",
		"  mcp       list, configure, or serve registered MCP servers (stdio or loopback HTTP)",
		"  codex     compile or check the Codex project mirror",
		"  opencode  compile, check, or inspect the OpenCode project mirror",
	} {
		fmt.Fprintln(w, line)
	}
}

func diagnosticCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == doctorCommand {
		return true
	}
	return args[0] == configCommand && len(args) > 1 && (args[1] == "show" || args[1] == "validate")
}

func runMCP(
	args []string,
	stdout, stderr io.Writer,
	runtime commandRuntime,
) (exitCode int) {
	// The installed wiring historically invokes bare `pfm mcp`; preserve that
	// argv as the chat server's serve action while making every new form named.
	if len(args) == 0 {
		args = []string{config.MCPServerChat, serveCommand}
	}
	if len(args) == 1 && args[0] == serveCommand {
		return runMCPServe(stdout, stderr, runtime, clock.Real)
	}
	if len(args) == 1 && args[0] == "ls" {
		for _, name := range config.RegisteredMCPServers() {
			server := runtime.Config.MCPServers[name]
			fmt.Fprintf(
				stdout,
				"%s\t%t\t%s\n",
				name,
				server.Enabled,
				runtime.Config.MCPServerSource(name),
			)
		}
		return 0
	}
	if len(args) < 2 || (len(args) > 2 && (args[0] != config.MCPServerHarvester || args[1] != serveCommand)) {
		fmt.Fprintln(stderr, "usage: pfm mcp ls | pfm mcp serve | pfm mcp <server> enable|disable|serve")
		return 2
	}
	name, action := args[0], args[1]
	server, registered := runtime.Config.MCPServers[name]
	if !registered {
		fmt.Fprintf(stderr, "pfm mcp: unknown server %q (run: pfm mcp ls)\n", name)
		return 2
	}
	if action == "enable" || action == "disable" {
		enabled := action == "enable"
		changed, err := config.SetMCPServer(runtime.Config, name, enabled)
		if err != nil {
			fmt.Fprintf(stderr, "pfm mcp %s %s: %v\n", name, action, err)
			return 1
		}
		state := "unchanged"
		if changed {
			state = "updated"
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", name, action+"d", state)
		return 0
	}
	if action != serveCommand {
		fmt.Fprintln(stderr, "usage: pfm mcp ls | pfm mcp <server> enable|disable|serve")
		return 2
	}
	if !server.Enabled {
		fmt.Fprintf(
			stderr,
			"pfm mcp %s: disabled by config %s; enable it with: pfm --config %s mcp %s enable\n",
			name,
			runtime.Config.Path,
			runtime.Config.Path,
			name,
		)
		return 1
	}
	if name == config.MCPServerHarvester {
		return runHarvesterMCP(args[2:], stdout, stderr, runtime)
	}
	if name != config.MCPServerChat {
		fmt.Fprintf(stderr, "pfm mcp %s: registered server has no implementation\n", name)
		return 1
	}
	service, err := mcpserv.NewConfigured(version, stderr, mcpRuntime(runtime, true))
	if err != nil {
		fmt.Fprintf(stderr, "pfm mcp: %v\n", err)
		return 1
	}
	defer func() { cli.CloseResource(service, "pfm mcp: close service", stderr, &exitCode) }()
	// This server answers from the build it started on until its chat ends:
	// Claude Code does not relaunch a stdio server that exits, so ending it on
	// an install would take the chat tools away from every running chat.
	err = service.RunStdio(context.Background(), os.Stdin, os.Stdout)
	if err != nil {
		fmt.Fprintf(stderr, "pfm mcp: %v\n", err)
		return 1
	}
	return 0
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	flags := cli.NewFlagSet(versionCommand, "usage: pfm version", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	fmt.Fprintf(stdout, "pfm %s\n", config.DisplayVersion(version))
	return 0
}

func runKill(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) (exitCode int) {
	flags := cli.NewFlagSet(
		"chat kill",
		"usage: pfm chat kill [self | id] [--exit]",
		stderr,
	)
	self := flags.Bool("self", false, "kill the calling tmux chat")
	exit := flags.Bool("exit", false, "require a live pane (a live chat is closed either way)")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() > 1 || (*self && flags.NArg() != 0) ||
		(!*self && flags.NArg() != 1) {
		flags.Usage()
		return 2
	}

	runtime, err := config.OptionalRuntime(runtimes)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat kill: config: %v\n", err)
		return 1
	}
	database, manager, code := fleet.OpenKillManager(stderr, runtime)
	if code != 0 {
		return code
	}
	defer func() { cli.CloseResource(database, "pfm chat kill: close database", stderr, &exitCode) }()
	ctx := context.Background()
	id := ""
	var engine pfmengine.ID
	rolloutPath := ""
	var socket, paneID string
	if flags.NArg() == 1 {
		id = flags.Arg(0)
		// The index may not have caught up with the row the picker already
		// shows — a fresh agent's transcript, a live Codex pane holding no
		// rollout file yet. A compose pass is the picker's own source of
		// truth for what exists right now, so resolving against it vouches
		// for exactly the ids the picker would let you ⌃X, and nothing else.
		// The same pass hands back the row's live tmux address so a kill of
		// a live-but-unindexed row still ends it, not just hides it.
		address, _, lookupErr := fleet.ResolveRow(ctx, database, id, stderr, &runtime)
		if lookupErr != nil {
			fmt.Fprintf(stderr, "pfm chat kill: lookup failed: %v\n", lookupErr)
			return 1
		}
		engine = address.Engine
		rolloutPath = address.RolloutPath
		socket = address.Socket
		paneID = address.PaneID
	}
	target, err := manager.Kill(ctx, kill.Request{
		ID:          id,
		Engine:      engine,
		RolloutPath: rolloutPath,
		SocketName:  socket,
		PaneID:      paneID,
		Self:        *self,
		Exit:        *exit,
		Environment: kill.Environment(paths.OSEnv{}),
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat kill: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, pfmchat.KillOutcome(
		target.ID, target.SocketName, target.PaneID,
		!pfmengine.SocketKeyedID(target.Engine, target.ID, target.SocketName),
	))
	return 0
}

func runUnkill(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) (exitCode int) {
	flags := cli.NewFlagSet("chat unkill", "usage: pfm chat unkill id", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	database, manager, code := fleet.OpenKillManager(stderr, runtimes...)
	if code != 0 {
		return code
	}
	defer func() { cli.CloseResource(database, "pfm chat unkill: close database", stderr, &exitCode) }()
	if err := manager.Unkill(context.Background(), flags.Arg(0)); err != nil {
		fmt.Fprintf(stderr, "pfm chat unkill: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "unkilled %s\n", flags.Arg(0))
	return 0
}

func runInternal(args []string, stdout, stderr io.Writer, runtime commandRuntime) (exitCode int) {
	stdout, finishHook := obs.Hook(context.Background(), obs.Verb(args), stdout)
	defer func() { finishHook(exitCode) }()
	if len(args) != 0 && args[0] == "clear-kill" {
		return hookentry.ClearKill(args[1:], os.Stdin, stderr, runtime)
	}
	if len(args) != 0 && args[0] == "agent-open" {
		return hookentry.AgentOpen(args[1:], stderr, runtime)
	}
	if len(args) != 0 && args[0] == "codex-launch" {
		return hookentry.CodexLaunch(args[1:], stderr)
	}
	if len(args) != 0 && args[0] == "claude-launch" {
		return hookentry.ClaudeLaunch(args[1:], stdout, stderr, runtime, nil)
	}
	if len(args) != 0 && args[0] == "launch" {
		return hookentry.Launch(args[1:], stdout, stderr, runtime, nil)
	}
	if len(args) != 0 && args[0] == "launcher-repair" {
		return hookentry.LauncherRepair(args[1:], stderr, runtime)
	}
	if len(args) != 0 && args[0] == "claude-version" {
		return hookentry.ClaudeVersion(args[1:], stdout, stderr, runtime)
	}
	if len(args) != 0 && args[0] == "explore-deny" {
		return hookentry.ExploreDeny(os.Stdin, stdout, stderr)
	}
	if len(args) != 0 && args[0] == "git-guard" {
		return hookentry.GitGuard(os.Stdin, stdout, stderr)
	}
	if len(args) != 0 && args[0] == "orchestrator-wait" {
		return hookentry.OrchestratorWait(os.Stdin, stdout, stderr)
	}
	if len(args) != 0 && args[0] == "callmeter" {
		return hookentry.Callmeter(os.Stdin, stderr, paths.OSEnv{})
	}
	if len(args) != 0 && args[0] == "rr-dir" {
		return runRRDirEntry(os.Stdin, stdout, stderr, paths.OSEnv{})
	}
	if len(args) != 0 && args[0] == "epic-inject" {
		return hookentry.EpicInject(os.Stdin, stdout, stderr)
	}
	if len(args) != 0 && args[0] == "reload-intercept" {
		reloadFront := func(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
			return runChatReloadWithRuntime(args, stdout, stderr, runtime, paths.OSEnv{})
		}
		return hookentry.ReloadIntercept(os.Stdin, stdout, stderr, runtime, reloadFront)
	}
	if len(args) != 0 && args[0] == "exit-intercept" {
		return hookentry.ExitIntercept(os.Stdin, stdout, stderr, runtime, runKill)
	}
	if len(args) != 0 && args[0] == "exit-close" {
		return hookentry.ExitClose(os.Stdin, stderr)
	}
	if len(args) != 0 && args[0] == "compact-nudge" {
		return hookentry.CompactNudge(os.Stdin, stdout, stderr, runtime, nil)
	}
	if len(args) != 0 && args[0] == "reload-run" {
		return runChatReloadWorkerWithRuntime(args[1:], os.Stdout, stderr, runtime, paths.OSEnv{})
	}
	if len(args) != 0 && args[0] == "then" {
		return hookentry.Then(args[1:], stderr, runtime)
	}
	if len(args) != 0 && args[0] == "tmux-title-renudge" {
		return hookentry.TmuxTitleRenudge(args[1:], stderr, runtime)
	}
	if len(args) != 0 && args[0] == "update-check" {
		return hookentry.UpdateCheck(args[1:], stderr)
	}
	if len(args) != 0 && args[0] == "primary-get" {
		primary, err := fleet.PrimaryAccount(runtime.Paths, runtime.Config)
		if err != nil {
			fmt.Fprintf(stderr, "pfm internal primary-get: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, primary)
		return 0
	}
	if len(args) != 0 && args[0] == "chat-server" {
		return hookentry.ChatServer(args[1:], stderr, runtime)
	}
	if len(args) != 0 && args[0] == "stale" {
		return stale.Run(args[1:], stdout, stderr)
	}
	if len(args) != 0 && args[0] == "statusline" {
		return runStatuslineWithRuntime(args[1:], os.Stdin, stdout, stderr, runtime, paths.OSEnv{})
	}
	if len(args) != 0 && args[0] == "primary-set" {
		flags := cli.NewFlagSet("internal primary-set", "usage: pfm internal primary-set <account>", stderr)
		if code, ok := cli.ParseFlags(flags, args[1:]); !ok {
			return code
		}
		if flags.NArg() != 1 {
			flags.Usage()
			return 2
		}
		account, err := strconv.Atoi(flags.Arg(0))
		if err != nil {
			flags.Usage()
			return 2
		}
		if err := fleet.SetPrimaryAccount(runtime.Paths, runtime.Config, account); err != nil {
			fmt.Fprintf(stderr, "pfm internal primary-set: %v\n", err)
			return 1
		}
		return 0
	}
	if len(args) == 0 {
		// Keep this literal pipe-joined for C15; the registry test checks branch reachability.
		fmt.Fprintln(
			stderr,
			"usage: pfm internal agent-open|callmeter|chat-server|claude-launch|claude-version|clear-kill|codex-launch|compact-nudge|epic-inject|exit-close|exit-intercept|explore-deny|git-guard|kill-exit|launch|launcher-repair|orchestrator-wait|primary-get|primary-set|reload-intercept|reload-run|rr-dir|stale|statusline|then|tmux-title-renudge|update-check [options]",
		)
		return 2
	}
	if args[0] != "kill-exit" {
		// Hooks and units are the only callers of an internal name, and exit 2
		// is Claude Code's BLOCKING hook code: a hook a different pfm version
		// registered (a rollback, a stale binary on PATH) would erase every
		// prompt or deny every tool call. An unknown name is a non-blocking
		// error that says what happened and how to converge.
		fmt.Fprintf(
			stderr,
			"pfm internal: unknown subcommand %q — registered by a different pfm version than this binary (%s); run `pfm install --yes` with the binary you intend to keep\n",
			args[0],
			config.DisplayVersion(version),
		)
		return 1
	}
	flags := cli.NewFlagSet(
		"internal kill-exit",
		"usage: pfm internal kill-exit --engine cc|cx --id id --path path --socket path --socket-name name --pane %id",
		stderr,
	)
	engine := flags.String("engine", "", "engine name")
	id := flags.String("id", "", "chat id")
	dataPath := flags.String("path", "", "transcript or rollout path")
	socket := flags.String("socket", "", "tmux socket path")
	socketName := flags.String("socket-name", "", "tmux socket basename")
	pane := flags.String("pane", "", "tmux pane id")
	if code, ok := cli.ParseFlags(flags, args[1:]); !ok {
		return code
	}
	if flags.NArg() != 0 || *engine == "" || *id == "" ||
		*socket == "" || *socketName == "" || *pane == "" {
		flags.Usage()
		return 2
	}
	engineID, err := pfmengine.Parse(*engine)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal kill-exit: %v\n", err)
		return 1
	}
	database, err := store.Open(store.WithWarningWriter(stderr))
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal kill-exit: %v\n", err)
		return 1
	}
	defer func() { cli.CloseResource(database, "pfm internal kill-exit: close database", stderr, &exitCode) }()
	finisher, err := kill.NewFinisher(database, kill.Dependencies{
		Paths:       runtime.Paths,
		ClaudeRoots: runtime.Config.ProjectRoots(),
		CodexHomes:  runtime.Config.CodexHomes(),
	})
	if err == nil {
		err = finisher.Run(context.Background(), kill.ExitArgs{
			Engine:     engineID,
			ID:         *id,
			DataPath:   *dataPath,
			SocketPath: *socket,
			SocketName: *socketName,
			PaneID:     *pane,
		})
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal kill-exit: %v\n", err)
		return 1
	}
	return 0
}

func runRRDirEntry(input io.Reader, stdout, stderr io.Writer, env paths.Env) int {
	home, err := env.Home()
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal rr-dir: resolve home directory: %v\n", err)
		home = ""
	}
	return hookentry.RRDir(input, stdout, stderr, home)
}
