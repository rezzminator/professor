package hookentry

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

const codexApplyCommand = "apply"

// The help and version flags every launcher passes straight through.
const (
	helpFlag    = "--help"
	versionFlag = "--version"
)

// codexPassthrough lists the arguments that make a Codex run no session: the launcher adds no persona.
var codexPassthrough = map[string]bool{
	"login": true, "logout": true, "mcp": true, "mcp-server": true, "app-server": true, "completion": true,
	"sandbox": true, "debug": true, codexApplyCommand: true, "cloud": true, "features": true, "help": true,
	versionFlag: true, "-V": true, helpFlag: true, "-h": true,
}

// codexValueFlags are Codex's top-level options that take the next argument as their value, so a
// value spelled like a subcommand (`--cd sandbox`, `-m review`) is never read as one.
var codexValueFlags = map[string]bool{
	"-c": true, "--config": true, "--enable": true, "--disable": true, "-i": true, "--image": true,
	"-m": true, "--model": true, "-p": true, "--profile": true, "-s": true, "--sandbox": true,
	"-a": true, "--ask-for-approval": true, "-C": true, "--cd": true, "--add-dir": true,
	"--local-provider": true,
}

// codexRunsAsInteractiveChat reports whether a Codex launch is the interactive chat of its pane. A
// help or version flag, or a pass-through subcommand, starts no chat; exec and review run
// non-interactive turns that fire the Codex hooks. The subcommand is the first positional argument
// (any other one is the prompt).
func codexRunsAsInteractiveChat(args []string) bool {
	subcommand, help := codexSubcommand(args)
	return !help && !codexPassthrough[subcommand] && subcommand != "exec" && subcommand != "review"
}

// codexSubcommand is a Codex launch's first positional argument, empty for none or past a "--";
// help reports a help or version flag before it.
func codexSubcommand(args []string) (subcommand string, help bool) {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == helpFlag || arg == "-h" || arg == versionFlag || arg == "-V":
			return "", true
		case arg == "--":
			return "", false
		case codexValueFlags[arg]:
			index++
		case strings.HasPrefix(arg, "-"):
		default:
			return arg, false
		}
	}
	return "", false
}

// CodexLaunch applies the current primary account's Codex policy at launch time.
func CodexLaunch(args []string, stderr io.Writer, runtime config.Runtime) int {
	primary, err := fleet.PrimaryAccount(runtime.Paths, runtime.Config)
	if err != nil {
		fmt.Fprintf(stderr, "resolve primary account: %v\n", err)
		return 1
	}
	policy := runtime.Config.EffectiveCodex(primary)
	binaryName := policy.Binary
	if binaryName == "" {
		binaryName = pfmengine.MustLookup(pfmengine.Codex).Binary
	}
	binary, err := obs.Runner(deps.RealRunner{}).LookPath(binaryName)
	if err != nil {
		fmt.Fprintf(stderr, "resolve Codex binary: %v\n", err)
		return 1
	}
	argv := []string{binary}
	if policy.Yolo {
		argv = append(argv, "--dangerously-bypass-approvals-and-sandbox")
	}
	personaArgs, err := codexWorkbenchArgs(args, runtime.Paths.Home)
	if err != nil {
		fmt.Fprintf(stderr, "launch Codex: %v\n", err)
		return 1
	}
	argv = append(argv, personaArgs...)
	argv = append(argv, args...)
	unset := make(map[string]bool)
	for _, name := range claudelaunch.Hygiene() {
		unset[name] = true
	}
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !unset[name] {
			env = append(env, entry)
		}
	}
	if !codexRunsAsInteractiveChat(args) {
		// A subcommand or a non-interactive turn is never the chat bound to the pane: its hooks must
		// not find the parent's Herdr pane, by Herdr name or by tmux pane.
		env = herdr.StripReporter(env)
	}
	if err := LaunchExec(binary, argv, env); err != nil {
		fmt.Fprintf(stderr, "launch Codex: %v\n", err)
		return 1
	}
	return 0
}

func codexWorkbenchArgs(args []string, home string) ([]string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve cwd: %w", err)
	}
	mode := workbench.New
	var model, effort, prompt bool
	for _, arg := range args {
		if codexPassthrough[arg] {
			return nil, nil
		}
		if arg == "resume" || arg == "fork" {
			mode = workbench.Resume
		}
		model = model || arg == "-m" || arg == "--model" || strings.HasPrefix(arg, "--model=")
		effort = effort || strings.Contains(arg, "model_reasoning_effort=")
		prompt = prompt || strings.Contains(arg, "developer_instructions=")
	}
	persona, err := action.WorkbenchPersona(cwd, pfmengine.Codex, mode)
	if err != nil {
		return nil, err
	}
	if !persona.Applies() {
		return nil, nil
	}
	if err := workbench.EnsureMirror(persona.Bench, pfmengine.Codex, home); err != nil {
		return nil, err
	}
	var inserted []string
	if persona.Model != "" && !model {
		inserted = append(inserted, "--model", persona.Model)
	}
	if persona.Effort != "" && !effort {
		inserted = append(inserted, action.CodexEffortArg(persona.Effort)...)
	}
	if !prompt {
		inserted = append(inserted, action.CodexDeveloperInstructionsArg(persona.Body)...)
	}
	return inserted, nil
}
