package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/codexgen"
)

const agentsCommand = "agents"

// runCodex is the deliberately small command adapter around the pure compiler.
// The compiler owns discovery and reconciliation; this layer owns argv, exit
// status, and the operator-visible report.
func runCodex(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		printCodexUsage(stderr)
		return 2
	}
	switch args[0] {
	case agentsCommand:
		return runCodexAgents(args[1:], stdout, stderr, runtime)
	case helpCommand, "-h", helpFlag:
		printCodexUsage(stdout)
		return 0
	}
	var mode codexgen.Mode
	switch args[0] {
	case "build":
		mode = codexgen.ModeBuild
	case checkAction:
		mode = codexgen.ModeCheck
	default:
		fmt.Fprintf(stderr, "pfm codex: unknown action %q\n", args[0])
		printCodexUsage(stderr)
		return 2
	}

	flags := cli.NewFlagSet("codex "+args[0], "usage: pfm codex "+args[0]+" [repo-root] [options]", stderr)
	home := flags.String("home", "", "Codex global source/output home")
	var models, excludeDirs, excludeProjects, neverRegister repeatString
	flags.Var(&models, "model", "model alias mapping alias=value; repeatable")
	rootAdapter := flags.String("root-adapter", "", "replace the generated root AGENTS adapter")
	agentPreamble := flags.String("agent-preamble", "", "replace the generated agent preamble")
	flags.Var(&excludeDirs, "exclude-dir", "exclude a command directory; repeatable")
	flags.Var(&excludeProjects, "exclude-project", "exclude a project; repeatable")
	flags.Var(&neverRegister, "never-register", "do not register an agent; repeatable")
	suffixMode := flags.String("suffix-mode", "", "agent suffix mode")
	suffixPrefix := flags.String("suffix-prefix", "", "agent suffix prefix")
	positionals, code, ok := cli.ParseFlagsAnywhere(flags, args[1:])
	if !ok {
		return code
	}
	if len(positionals) > 1 {
		flags.Usage()
		return 2
	}

	root := ""
	if len(positionals) == 1 {
		root = positionals[0]
	}
	var err error
	if root == "" {
		root, err = codexRepoRoot()
	} else {
		root, err = filepath.Abs(root)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm codex %s: resolve repo root: %v\n", args[0], err)
		return 1
	}
	resolvedHome := *home
	if resolvedHome == "" {
		resolvedHome = runtime.Paths.Home
	}
	resolvedHome, err = filepath.Abs(resolvedHome)
	if err != nil {
		fmt.Fprintf(stderr, "pfm codex %s: resolve home: %v\n", args[0], err)
		return 1
	}

	overrides, err := codexCLIOverrides(
		models,
		*rootAdapter,
		*agentPreamble,
		excludeDirs,
		excludeProjects,
		neverRegister,
		*suffixMode,
		*suffixPrefix,
	)
	if err != nil {
		fmt.Fprintf(stderr, "pfm codex %s: %v\n", args[0], err)
		return 2
	}
	flags.Visit(func(seen *flag.Flag) {
		switch seen.Name {
		case "root-adapter":
			overrides.SetRootAdapter = true
		case "agent-preamble":
			overrides.SetAgentPreamble = true
		}
	})
	options := codexgen.Options{Root: root, Home: resolvedHome, Mode: mode, CLIOverrides: overrides}
	result, err := codexgen.Run(options)
	if err != nil {
		fmt.Fprintf(stderr, "pfm codex %s: %v\n", args[0], err)
		return 1
	}
	printCodexResult(stdout, stderr, result)
	if !result.OK {
		return 1
	}
	fmt.Fprintf(stdout, "CODEX %s PASS\n", strings.ToUpper(args[0]))
	return 0
}

func printCodexUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: pfm codex build|check|agents [repo-root] [options]")
	fmt.Fprintln(w, "  --home PATH")
	fmt.Fprintln(w, "  --model alias=value       repeatable")
	fmt.Fprintln(w, "  --root-adapter TEXT       replace root adapter")
	fmt.Fprintln(w, "  --agent-preamble TEXT     replace agent preamble")
	fmt.Fprintln(w, "  --exclude-dir NAME        repeatable")
	fmt.Fprintln(w, "  --exclude-project NAME    repeatable")
	fmt.Fprintln(w, "  --never-register NAME     repeatable")
	fmt.Fprintln(w, "  --suffix-mode MODE [--suffix-prefix TEXT]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "usage: pfm codex agents [--home PATH]")
	fmt.Fprintln(w, "  compiles every {home}/.professor/templates/global/agents/*.md into a sibling .toml,")
	fmt.Fprintln(w, "  then symlinks {home}/.claude/agents to the .md sources and {home}/.codex/agents")
	fmt.Fprintln(w, "  to the compiled .tomls — the global (host-level) agent registry.")
}

// runCodexAgents is the command adapter for the global (host-level) Codex
// agent compiler — the Go port of the retired
// ~/.professor/templates/global/agents/build-global-agents.py. Unlike build/check it has no
// repository root: its source and destinations are all anchored on --home
// (default: this process's resolved HOME).
func runCodexAgents(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	flags := cli.NewFlagSet("codex agents", "usage: pfm codex agents [--home PATH]", stderr)
	home := flags.String("home", "", "host HOME whose global agents get compiled and installed")
	positionals, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(positionals) != 0 {
		flags.Usage()
		return 2
	}
	resolvedHome := *home
	if resolvedHome == "" {
		resolvedHome = runtime.Paths.Home
	}
	resolvedHome, err := filepath.Abs(resolvedHome)
	if err != nil {
		fmt.Fprintf(stderr, "pfm codex agents: resolve home: %v\n", err)
		return 1
	}
	result, err := codexgen.RunGlobalAgents(codexgen.GlobalAgentsOptions{Home: resolvedHome})
	if err != nil {
		fmt.Fprintf(stderr, "pfm codex agents: %v\n", err)
		return 1
	}
	for _, compiled := range result.Compiled {
		fmt.Fprintf(stdout, "%s: %d B, parses clean\n", compiled.Path, compiled.Size)
	}
	for _, installed := range result.Installed {
		fmt.Fprintf(
			stdout,
			"%s %s\n",
			installed.State,
			codexgen.DescribeGlobalLinkState(installed.State, installed.Path, installed.Source, installed.Found),
		)
	}
	for _, problem := range result.Problems {
		fmt.Fprintf(stderr, "pfm codex agents: %s\n", problem)
	}
	fmt.Fprintln(stdout, "CODEX AGENTS PASS")
	return 0
}

func codexRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	fallback := ""
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		if fallback == "" {
			if info, statErr := os.Stat(
				filepath.Join(dir, claudeInstructionsFile),
			); statErr == nil &&
				info.Mode().IsRegular() {
				fallback = dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			if fallback != "" {
				return fallback, nil
			}
			return "", errors.New("no repository root found (expected .git or CLAUDE.md)")
		}
		dir = parent
	}
}

type repeatString []string

func (list *repeatString) String() string { return strings.Join(*list, ",") }
func (list *repeatString) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("repeated option value must be non-empty")
	}
	*list = append(*list, value)
	return nil
}

func codexCLIOverrides(
	models repeatString,
	adapter, preamble string,
	dirs, projects, agents repeatString,
	suffixMode, suffixPrefix string,
) (codexgen.CLIOverrides, error) {
	result := codexgen.CLIOverrides{
		RootAdapter:     adapter,
		AgentPreamble:   preamble,
		ExcludeDirs:     []string(dirs),
		ExcludeProjects: []string(projects),
		NeverRegister:   []string(agents),
		SuffixMode:      suffixMode,
		SuffixPrefix:    suffixPrefix,
	}
	modelMap := make(map[string]string, len(models))
	for _, item := range models {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return result, fmt.Errorf("--model requires alias=value, got %q", item)
		}
		modelMap[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	if len(modelMap) != 0 {
		result.ModelMap = modelMap
	}
	return result, nil
}

func printCodexResult(_, stderr io.Writer, result codexgen.Result) {
	for _, warning := range result.Warnings {
		fmt.Fprintf(stderr, "pfm codex: warning: %s\n", warning)
	}
	for _, problem := range result.Problems {
		fmt.Fprintf(stderr, "pfm codex: %s\n", problem)
	}
}

var _ flag.Value = (*repeatString)(nil)
