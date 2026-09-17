// Command pfm manages live and resumable Claude and Codex chats.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"

	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/config"
	"hostops/pfm/internal/doctor"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/installer"
	"hostops/pfm/internal/kill"
	"hostops/pfm/internal/mcpserv"
	"hostops/pfm/internal/picker"
	"hostops/pfm/internal/spawn"
	"hostops/pfm/internal/stale"
	"hostops/pfm/internal/store"
	"hostops/pfm/internal/update"
)

const (
	chatCommand      = "chat"
	initCommand      = "init"
	indexCommand     = "index"
	headlessCommand  = "headless"
	whoamiCommand    = "whoami"
	versionCommand   = "version"
	configCommand    = "config"
	archiveCommand   = "archive"
	internalCommand  = "internal"
	reloadRunCommand = "reload-run"
	serveCommand     = "serve"
	installCommand   = "install"
	mcpCommand       = "mcp"
	updateCommand    = "update"
	doctorCommand    = "doctor"
	checkAction      = "check"
)

var version = config.DevelopmentVersion

// topLevelSubcommands names every case the switch in run dispatches by
// argv[0] — the single source both TestTopLevelSubcommandsReachTheirHandler
// and installer.SetImplementedSubcommands read, so the installer's
// unknown-pfm-hook predicate (issue #24 F1) can never drift from what this
// binary actually implements.
var topLevelSubcommands = []string{
	versionCommand, "ls", chatCommand, "harvest", headlessCommand, indexCommand, doctorCommand,
	configCommand, "reap", archiveCommand, "heal", "name-sync", "statusline",
	"usage-hook", installCommand, "uninstall", updateCommand, initCommand, whoamiCommand,
	"issues", mcpCommand, pfmengine.MustLookup(pfmengine.Codex).LongName, internalCommand,
}

// internalSubcommands names every case runInternal dispatches by args[0] —
// the usage line below and TestInternalSubcommandsReachTheirHandler both
// derive from this one list, alongside installer.SetImplementedSubcommands.
var internalSubcommands = []string{
	"agent-open", "chat-server", "claude-version", "clear-kill",
	"codex-appendix", "codex-launch", "compact-nudge", "epic-inject",
	"exit-close", "exit-intercept", "explore-deny", "kill-exit", "launch",
	"launcher-repair", "primary-get", "primary-set", "reload-intercept",
	reloadRunCommand, "stale", thenAction, "update-check",
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

func run(args []string, stdout, stderr io.Writer) int {
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
		return runChatWithRuntime(args[1:], os.Stdin, stdout, stderr, runtime)
	case "harvest":
		return runHarvest(args[1:], stdout, stderr, runtime)
	case "headless":
		return runHeadless(args[1:], stdout, stderr, runtime)
	case "index":
		return runIndex(args[1:], stdout, stderr, runtime)
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
		return runStatuslineWithRuntime(args[1:], os.Stdin, stdout, stderr, runtime)
	case "usage-hook":
		return runUsageHookWithRuntime(args[1:], stdout, stderr, runtime)
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
		return runMCPServe(stdout, stderr, runtime)
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
	if err := service.RunStdio(
		context.Background(),
		os.Stdin,
		os.Stdout,
	); err != nil {
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
	fmt.Fprintf(stdout, "pfm %s\n", displayVersion())
	return 0
}

// displayVersion resolves the reported version. A release build stamps
// `version` via ldflags (`-X main.version=...`, see Makefile `host-install`);
// an unstamped build — `go build ./cmd/pfm` with no ldflags — leaves it at
// "dev", which alone tells nobody which commit they are running. Go itself
// already answers that: since 1.18 the toolchain embeds VCS info in every
// build's own binary, ldflags or not, so falling back to it turns an
// unstamped "dev" into a build the operator can still identify.
func displayVersion() string {
	if version != config.DevelopmentVersion {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	return resolveDevVersion(info.Settings)
}

// resolveDevVersion is the pure half of displayVersion, split out so a test
// can drive it with fabricated settings instead of needing a real
// VCS-stamped binary (go test's own binary carries none — see main_test.go).
func resolveDevVersion(settings []debug.BuildSetting) string {
	var revision string
	var modified bool
	for _, setting := range settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision == "" {
		return config.DevelopmentVersion
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified {
		return fmt.Sprintf("dev (%s, modified)", revision)
	}
	return fmt.Sprintf("dev (%s)", revision)
}

func runKill(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) (exitCode int) {
	flags := cli.NewFlagSet(
		"chat kill",
		"usage: pfm chat kill [self | id] [--exit]",
		stderr,
	)
	self := flags.Bool("self", false, "kill the calling tmux chat")
	exit := flags.Bool("exit", false, "gracefully close after killing")
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
		engine, rolloutPath, socket, paneID = fleet.ResolveRow(ctx, database, id, stderr, &runtime)
	}
	target, err := manager.Kill(ctx, kill.Request{
		ID:          id,
		Engine:      engine,
		RolloutPath: rolloutPath,
		SocketName:  socket,
		PaneID:      paneID,
		Self:        *self,
		Exit:        *exit,
		Environment: kill.Environment(),
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat kill: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "killed %s\n", target.ID)
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

func runInternal(
	args []string,
	stdout, stderr io.Writer,
	runtime commandRuntime,
) (exitCode int) {
	if len(args) != 0 && args[0] == "clear-kill" {
		return runClearKill(args[1:], os.Stdin, stderr, runtime)
	}
	if len(args) != 0 && args[0] == "agent-open" {
		return runInternalAgentOpen(args[1:], stderr, runtime)
	}
	if len(args) != 0 && args[0] == "codex-launch" {
		return runCodexLaunchCompatibility(args[1:], stderr)
	}
	if len(args) != 0 && args[0] == "codex-appendix" {
		return runCodexAppendix(os.Stdin, stdout, stderr, runtime)
	}
	if len(args) != 0 && args[0] == "launch" {
		return runInternalLaunch(args[1:], stdout, stderr, runtime)
	}
	if len(args) != 0 && args[0] == "launcher-repair" {
		return runInternalLauncherRepair(args[1:], stderr, runtime)
	}
	if len(args) != 0 && args[0] == "claude-version" {
		return runInternalClaudeVersion(args[1:], stdout, stderr, runtime)
	}
	if len(args) != 0 && args[0] == "explore-deny" {
		return runExploreDeny(os.Stdin, stdout, stderr)
	}
	if len(args) != 0 && args[0] == "epic-inject" {
		return runEpicInject(os.Stdin, stdout, stderr)
	}
	if len(args) != 0 && args[0] == "reload-intercept" {
		return runReloadIntercept(os.Stdin, stdout, stderr, runtime)
	}
	if len(args) != 0 && args[0] == "exit-intercept" {
		return runExitIntercept(os.Stdin, stdout, stderr, runtime)
	}
	if len(args) != 0 && args[0] == "exit-close" {
		return runExitClose(os.Stdin, stderr)
	}
	if len(args) != 0 && args[0] == "compact-nudge" {
		return runCompactNudge(os.Stdin, stdout, stderr, runtime)
	}
	if len(args) != 0 && args[0] == "reload-run" {
		return runChatReloadWorkerWithRuntime(args[1:], os.Stdout, stderr, runtime)
	}
	if len(args) != 0 && args[0] == "then" {
		return runInternalThen(args[1:], stderr, runtime)
	}
	if len(args) != 0 && args[0] == "update-check" {
		return runInternalUpdateCheck(args[1:], stderr)
	}
	if len(args) != 0 && args[0] == "primary-get" {
		fmt.Fprintln(stdout, fleet.PrimaryAccount(runtime.Paths, runtime.Config))
		return 0
	}
	if len(args) != 0 && args[0] == "chat-server" {
		return runInternalChatServer(args[1:], stderr, runtime)
	}
	if len(args) != 0 && args[0] == "stale" {
		return stale.Run(args[1:], stdout, stderr)
	}
	if len(args) != 0 && args[0] == "primary-set" {
		flags := cli.NewFlagSet(
			"internal primary-set",
			"usage: pfm internal primary-set <account>",
			stderr,
		)
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
		// This literal stays in sync with internalSubcommands by
		// construction, not derivation: scripts/arch-check.sh's C15 check
		// greps the pipe-joined subcommand names out of main.go's raw
		// source text below, so a runtime-joined string here (fine for Go,
		// blind to a static grep) would defeat that ratchet.
		// TestInternalSubcommandsReachTheirHandler is the runtime half of
		// the same guarantee: every internalSubcommands name reaches a real
		// branch in runInternal's if-chain below.
		fmt.Fprintln(
			stderr,
			"usage: pfm internal agent-open|chat-server|claude-version|clear-kill|codex-appendix|codex-launch|compact-nudge|epic-inject|exit-close|exit-intercept|explore-deny|kill-exit|launch|launcher-repair|primary-get|primary-set|reload-intercept|reload-run|stale|then|update-check [options]",
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
			displayVersion(),
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

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: pfm [--config PATH] <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "operator commands:")
	fmt.Fprintln(w, "  ls        list or pick fleet chats")
	fmt.Fprintln(w, "  chat      operate on one chat: new, open, inject, ask, read, stream, name, kill, end")
	fmt.Fprintln(w, "  headless  run Claude or Codex through one isolated process interface")
	fmt.Fprintln(w, "  harvest   fetch and convert URL, DOI, ISBN, PMID, PMCID, or local path")
	fmt.Fprintln(w, "  index     refresh the transcript index")
	fmt.Fprintln(w, "  whoami    print this chat's own tmux session name")
	fmt.Fprintln(w, "  issues    list servicedesk complaints filed through issue_servicedesk")
	fmt.Fprintln(w, "  reap      classify the socket graveyard; --apply reclaims it")
	fmt.Fprintln(w, "  archive   move killed chats and old subagent transcripts out of sight, reversibly")
	fmt.Fprintln(w, "  heal      report or repair wedged Codex history projections")
	fmt.Fprintln(w, "  install   wire or remove the self-contained host integration")
	fmt.Fprintln(w, "  uninstall remove the self-contained host integration")
	fmt.Fprintln(w, "  update    update the binary; check, adopt, pin, ignore, or drop project template baselines")
	fmt.Fprintln(w, "  init      scaffold project templates once and pin their baselines")
	fmt.Fprintln(w, "  config    initialize, inspect, or validate machine configuration")
	fmt.Fprintln(w, "  doctor    inspect fleet database and jail health")
	fmt.Fprintln(w, "  version   print the pfm version")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "wiring commands:")
	fmt.Fprintln(w, "  name-sync converge live chat window names")
	fmt.Fprintln(w, "  statusline render the native Claude status line")
	fmt.Fprintln(w, "  usage-hook the fail-open usage-limit prompt hook")
	fmt.Fprintln(w, "  mcp       list, configure, or serve registered MCP servers (stdio or loopback HTTP)")
	fmt.Fprintln(w, "  codex     compile or check the Codex project mirror")
}
