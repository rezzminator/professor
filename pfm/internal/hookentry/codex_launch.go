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
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

const codexApplyCommand = "apply"

// The help and version flags every launcher passes straight through.
const (
	helpFlag    = "--help"
	versionFlag = "--version"
)

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
		switch arg {
		case "login",
			"logout",
			"mcp",
			"mcp-server",
			"app-server",
			"completion",
			"sandbox",
			"debug",
			codexApplyCommand,
			"cloud",
			"features",
			"help",
			versionFlag,
			"-V",
			helpFlag,
			"-h":
			return nil, nil
		case "resume", "fork":
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
