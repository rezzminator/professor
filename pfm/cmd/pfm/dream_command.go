package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"hostops/pfm/internal/action"
	"hostops/pfm/internal/dream"
)

const defaultDreamAgent = "Explore"

var defaultDreamRegistry = filepath.Join(defaultDreamHome(), ".claude", "projects")

func defaultDreamHome() string {
	home, err := os.UserHomeDir()
	if err == nil && filepath.IsAbs(home) {
		return filepath.Clean(home)
	}
	// Parsing still needs deterministic defaults if the host cannot resolve a
	// user directory. Production operations will fail closed on these absent
	// absolute paths instead of inheriting an ambiguous relative location.
	return filepath.Join(string(filepath.Separator), "nonexistent-dream-home")
}

func defaultDreamConfigHome() string {
	if root := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	return filepath.Join(defaultDreamHome(), ".config")
}

func defaultDreamRepositoriesFile() string {
	return filepath.Join(defaultDreamConfigHome(), "pfm", "repos.list")
}

type dreamNightOptions struct {
	RepoRoot       string
	AgentType      string
	RegistryBase   string
	ResourcesRoot  string
	BootstrapCount int
	CorpusFile     string
	StartedAt      time.Time
}

type dreamApplyOptions struct {
	RepoRoot      string
	AgentType     string
	RegistryBase  string
	ResourcesRoot string
	Stage         string
}

type dreamInspectOptions struct {
	RepoRoot      string
	AgentType     string
	RegistryBase  string
	ResourcesRoot string
}

type dreamMorningOptions struct {
	RegistryBase     string
	ResourcesRoot    string
	RepositoriesFile string
}

type dreamMorningOutput struct {
	Stdout string
	Stderr string
	Failed bool
}

// dreamCommandRuntime keeps parsing and wire rendering independently testable
// from a real night. The production adapters below are the command package's
// only contact with internal/dream; no dream implementation package crosses
// the one-way import boundary.
type dreamCommandRuntime struct {
	night          func(context.Context, dreamNightOptions, io.Writer, io.Writer) (string, error)
	apply          func(context.Context, dreamApplyOptions) (string, error)
	inspect        func(dreamInspectOptions) (string, error)
	morning        func(context.Context, dreamMorningOptions) (dreamMorningOutput, error)
	migrate        func(string) (string, error)
	restamp        func(mapArgument, workingDirectory string, now time.Time) (string, error)
	hook           func(dreamHookRequest) ([]byte, error)
	now            func() time.Time
	project        func() string
	repositoryRoot func() (string, error)
}

func productionDreamRuntime(codexBinaries ...string) dreamCommandRuntime {
	codexBinary := ""
	configPath := ""
	if len(codexBinaries) != 0 {
		codexBinary = codexBinaries[0]
	}
	if len(codexBinaries) > 1 {
		configPath = codexBinaries[1]
	}
	return dreamCommandRuntime{
		night: func(
			ctx context.Context,
			options dreamNightOptions,
			stdout, stderr io.Writer,
		) (string, error) {
			request := dream.NightRequest{
				RepoRoot:      options.RepoRoot,
				RegistryBase:  options.RegistryBase,
				ResourcesRoot: options.ResourcesRoot,
				AgentType:     options.AgentType,
				StartedAt:     options.StartedAt,
			}
			request.Selection.BootstrapCount = options.BootstrapCount
			request.Selection.CorpusFile = options.CorpusFile
			dependencies := dream.DefaultNightDependenciesWithCodex(codexBinary)
			dependencies.Stdout = stdout
			dependencies.Stderr = stderr
			result, err := dream.Night(ctx, request, dependencies)
			if err != nil {
				return "", err
			}
			if result.Empty || !result.ApplyEligible {
				return "", nil
			}
			return renderDreamApplyCommand(options, result.Stage.Root, configPath), nil
		},
		apply: func(ctx context.Context, options dreamApplyOptions) (string, error) {
			result, err := dream.Apply(ctx, dream.ApplyRequest{
				RepoRoot:      options.RepoRoot,
				AgentType:     options.AgentType,
				RegistryBase:  options.RegistryBase,
				ResourcesRoot: options.ResourcesRoot,
				Stage:         options.Stage,
			})
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(
				"dreamer-night: APPLIED stage=%s sweep=%s — organ files written, uncommitted by design\n",
				options.Stage,
				result.Sweep,
			), nil
		},
		inspect: func(options dreamInspectOptions) (string, error) {
			repository, _, _, err := dream.InspectLane(
				options.RepoRoot,
				options.AgentType,
				options.RegistryBase,
				options.ResourcesRoot,
			)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(
				"REPO\t%s\nORGAN\t%s\nREGISTRY\t%s\n",
				repository.RepoRoot,
				repository.Organ,
				repository.Registry,
			), nil
		},
		morning: func(ctx context.Context, options dreamMorningOptions) (dreamMorningOutput, error) {
			result, err := dream.Morning(ctx, dream.MorningRequest{
				RegistryBase:     options.RegistryBase,
				ResourcesRoot:    options.ResourcesRoot,
				RepositoriesFile: options.RepositoriesFile,
			})
			return dreamMorningOutput{
				Stdout: result.Stdout,
				Stderr: result.Stderr,
				Failed: result.Failed,
			}, err
		},
		migrate: func(organ string) (string, error) {
			result, err := dream.MigrateAnchors(organ)
			if err != nil {
				return dream.RenderMigrationOutcomes(result), err
			}
			return dream.RenderMigrationResult(result), nil
		},
		restamp: dream.Restamp,
		hook: func(request dreamHookRequest) ([]byte, error) {
			return dream.Hook(dream.HookRequest{
				Kind:             dream.HookKind(request.Kind),
				Input:            request.Input,
				ProjectDirectory: request.ProjectDirectory,
				Now:              request.Now,
			})
		},
		now: time.Now,
		project: func() string {
			return os.Getenv("CLAUDE_PROJECT_DIR")
		},
		repositoryRoot: func() (string, error) {
			workingDirectory, err := os.Getwd()
			if err != nil {
				return "", fmt.Errorf("get working directory: %w", err)
			}
			return dream.RepositoryRoot(workingDirectory)
		},
	}
}

func renderDreamApplyCommand(options dreamNightOptions, stage string, configPaths ...string) string {
	configArgument := ""
	if len(configPaths) != 0 && configPaths[0] != "" {
		configArgument = " --config " + action.Quote(configPaths[0])
	}
	resourcesArgument := ""
	if options.ResourcesRoot != "" {
		resourcesArgument = " --resources " + options.ResourcesRoot
	}
	return fmt.Sprintf(
		"dreamer-night: signed apply command: pfm%s dream apply --repo %s --agent %s%s %s\n",
		configArgument,
		options.RepoRoot,
		options.AgentType,
		resourcesArgument,
		stage,
	)
}

func runDreamConfigured(
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	runtime commandRuntime,
) int {
	ctx, stop := dreamCommandContext()
	defer stop()
	return runDreamWith(
		ctx,
		args,
		stdin,
		stdout,
		stderr,
		productionDreamRuntime(runtime.Config.Codex.Binary, runtime.Config.Path),
	)
}

func dreamCommandContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func runDreamWith(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	runtime dreamCommandRuntime,
) int {
	if len(args) == 0 {
		printDreamUsage(stderr)
		return 2
	}
	switch args[0] {
	case "night":
		return runDreamNight(ctx, args[1:], stdout, stderr, runtime)
	case "apply":
		return runDreamApply(ctx, args[1:], stdout, stderr, runtime)
	case "inspect":
		return runDreamInspect(args[1:], stdout, stderr, runtime)
	case "morning":
		return runDreamMorning(ctx, args[1:], stdout, stderr, runtime)
	case "migrate-anchors":
		return runDreamMigrate(args[1:], stdout, stderr, runtime)
	case "restamp":
		return runDreamRestamp(args[1:], stdout, stderr, runtime)
	case "hook":
		return runDreamHook(args[1:], stdin, stdout, stderr, runtime)
	case "help", "-h", "--help":
		printDreamUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "pfm dream: unknown command %q\n", args[0])
		printDreamUsage(stderr)
		return 2
	}
}

func runDreamNight(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	runtime dreamCommandRuntime,
) int {
	flags := newFlagSet(
		"dream night",
		"usage: pfm dream night [--repo ROOT] [--resources DIR] [--agent TYPE] [--bootstrap-count N | --corpus-file FILE]",
		stderr,
	)
	repo := flags.String("repo", "", "repository root (default: current repository)")
	resourceRoot := flags.String("resources", "", "development overlay; embedded resources remain fallback")
	agent := flags.String("agent", defaultDreamAgent, "agent type to harvest")
	bootstrap := flags.String("bootstrap-count", "", "maximum transcripts for a bootstrap night")
	corpusFile := flags.String("corpus-file", "", "explicit newline-delimited transcript path file")
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	selectionSet := make(map[string]bool, 2)
	flags.Visit(func(value *flag.Flag) {
		selectionSet[value.Name] = true
	})
	bootstrapSet := selectionSet["bootstrap-count"]
	corpusSet := selectionSet["corpus-file"]
	if flags.NArg() != 0 || (bootstrapSet && corpusSet) {
		flags.Usage()
		return 2
	}
	bootstrapCount, ok := parseBootstrapCount(*bootstrap, bootstrapSet)
	if !ok || (corpusSet && (*corpusFile == "" || !filepath.IsAbs(*corpusFile))) {
		flags.Usage()
		return 2
	}
	repoRoot, err := resolveDreamRepo(*repo, runtime)
	if err != nil {
		return finishDreamCommand("night", "", err, stdout, stderr)
	}
	output, err := runtime.night(ctx, dreamNightOptions{
		RepoRoot:       repoRoot,
		AgentType:      *agent,
		RegistryBase:   defaultDreamRegistry,
		ResourcesRoot:  *resourceRoot,
		BootstrapCount: bootstrapCount,
		CorpusFile:     *corpusFile,
		StartedAt:      runtime.now(),
	}, stdout, stderr)
	return finishDreamCommand("night", output, err, stdout, stderr)
}

func parseBootstrapCount(raw string, specified bool) (int, bool) {
	if !specified {
		return 0, true
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value > 0
}

func resolveDreamRepo(explicit string, runtime dreamCommandRuntime) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if runtime.repositoryRoot == nil {
		return "", errors.New("resolve current repository; pass --repo ROOT: repository resolver is unavailable")
	}
	root, err := runtime.repositoryRoot()
	if err != nil {
		return "", fmt.Errorf("resolve current repository; pass --repo ROOT: %w", err)
	}
	return root, nil
}

func runDreamApply(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	runtime dreamCommandRuntime,
) int {
	flags := newFlagSet(
		"dream apply",
		"usage: pfm dream apply [--repo ROOT] [--resources DIR] [--agent TYPE] STAGE",
		stderr,
	)
	repo := flags.String("repo", "", "repository root (default: current repository)")
	resourceRoot := flags.String("resources", "", "development overlay; embedded resources remain fallback")
	agent := flags.String("agent", defaultDreamAgent, "agent type that owns the stage")
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	repoRoot, err := resolveDreamRepo(*repo, runtime)
	if err != nil {
		return finishDreamCommand("apply", "", err, stdout, stderr)
	}
	output, err := runtime.apply(ctx, dreamApplyOptions{
		RepoRoot:      repoRoot,
		AgentType:     *agent,
		RegistryBase:  defaultDreamRegistry,
		ResourcesRoot: *resourceRoot,
		Stage:         flags.Arg(0),
	})
	return finishDreamCommand("apply", output, err, stdout, stderr)
}

func runDreamInspect(
	args []string,
	stdout, stderr io.Writer,
	runtime dreamCommandRuntime,
) int {
	flags := newFlagSet(
		"dream inspect",
		"usage: pfm dream inspect [--repo ROOT] [--resources DIR] [--agent TYPE]",
		stderr,
	)
	repo := flags.String("repo", "", "repository root (default: current repository)")
	resourceRoot := flags.String("resources", "", "development overlay; embedded resources remain fallback")
	agent := flags.String("agent", defaultDreamAgent, "agent type to inspect")
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	repoRoot, err := resolveDreamRepo(*repo, runtime)
	if err != nil {
		return finishDreamCommand("inspect", "", err, stdout, stderr)
	}
	output, err := runtime.inspect(dreamInspectOptions{
		RepoRoot:      repoRoot,
		AgentType:     *agent,
		RegistryBase:  defaultDreamRegistry,
		ResourcesRoot: *resourceRoot,
	})
	return finishDreamCommand("inspect", output, err, stdout, stderr)
}

func runDreamMorning(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	runtime dreamCommandRuntime,
) int {
	flags := newFlagSet(
		"dream morning",
		"usage: pfm dream morning [--repos FILE] [--resources DIR]",
		stderr,
	)
	repositoriesFile := flags.String("repos", defaultDreamRepositoriesFile(), "repository list")
	resourceRoot := flags.String("resources", "", "development overlay; embedded resources remain fallback")
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	output, err := runtime.morning(ctx, dreamMorningOptions{
		RegistryBase:     defaultDreamRegistry,
		ResourcesRoot:    *resourceRoot,
		RepositoriesFile: *repositoriesFile,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm dream morning: %v\n", err)
		return 1
	}
	if _, err := io.WriteString(stdout, output.Stdout); err != nil {
		fmt.Fprintf(stderr, "pfm dream morning: write stdout: %v\n", err)
		return 1
	}
	if _, err := io.WriteString(stderr, output.Stderr); err != nil {
		return 1
	}
	if output.Failed {
		return 1
	}
	return 0
}

func runDreamMigrate(
	args []string,
	stdout, stderr io.Writer,
	runtime dreamCommandRuntime,
) int {
	flags := newFlagSet(
		"dream migrate-anchors",
		"usage: pfm dream migrate-anchors ORGAN",
		stderr,
	)
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	output, err := runtime.migrate(flags.Arg(0))
	if output != "" {
		if _, writeErr := io.WriteString(stdout, output); writeErr != nil {
			fmt.Fprintf(stderr, "pfm dream migrate-anchors: write stdout: %v\n", writeErr)
			return 1
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm dream migrate-anchors: %v\n", err)
		return 1
	}
	return 0
}

func runDreamHook(
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	runtime dreamCommandRuntime,
) int {
	flags := newFlagSet(
		"dream hook",
		"usage: pfm dream hook agent-inject|codex-subagent-inject|nudge",
		stderr,
	)
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	kind := dreamHookKind(flags.Arg(0))
	if kind != dreamHookAgentInject && kind != dreamHookCodexSubagentInject && kind != dreamHookNudge {
		flags.Usage()
		return 2
	}
	// The nudge derives everything from the environment and the organ and never
	// consumes the hook payload (internal/dream.Hook routes HookNudge to
	// nudgeHook without Input). Reading a stdin it does not use turns every
	// caller that leaves stdin open into a hang that SIGTERM cannot reclaim,
	// because goroutine 1 parks in the read on a locked thread — so `timeout`,
	// including the one configured in ~/.codex/hooks.json, does not clear it.
	var input []byte
	if kind != dreamHookNudge {
		payload, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "pfm dream hook %s: read stdin: %v\n", kind, err)
			return 1
		}
		input = payload
	}
	output, err := runtime.hook(dreamHookRequest{
		Kind:             kind,
		Input:            input,
		ProjectDirectory: runtime.project(),
		Now:              runtime.now(),
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm dream hook %s: %v\n", kind, err)
		return 1
	}
	if _, err := stdout.Write(output); err != nil {
		fmt.Fprintf(stderr, "pfm dream hook %s: write stdout: %v\n", kind, err)
		return 1
	}
	return 0
}

type dreamHookKind string

const (
	dreamHookAgentInject         dreamHookKind = "agent-inject"
	dreamHookCodexSubagentInject dreamHookKind = "codex-subagent-inject"
	dreamHookNudge               dreamHookKind = "nudge"
)

type dreamHookRequest struct {
	Kind             dreamHookKind
	Input            []byte
	ProjectDirectory string
	Now              time.Time
}

func finishDreamCommand(
	command, output string,
	err error,
	stdout, stderr io.Writer,
) int {
	if err != nil {
		fmt.Fprintf(stderr, "pfm dream %s: %v\n", command, err)
		return 1
	}
	if _, err := io.WriteString(stdout, output); err != nil {
		fmt.Fprintf(stderr, "pfm dream %s: write stdout: %v\n", command, err)
		return 1
	}
	return 0
}

func printDreamUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: pfm dream <command> [options]")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "commands:")
	fmt.Fprintln(writer, "  night             run one distill/refine night")
	fmt.Fprintln(writer, "  apply             apply a gated stage")
	fmt.Fprintln(writer, "  inspect           inspect repository and organ context")
	fmt.Fprintln(writer, "  morning           run configured repositories sequentially")
	fmt.Fprintln(writer, "  migrate-anchors   translate legacy anchor rows")
	fmt.Fprintln(writer, "  restamp           re-resolve one map's anchors at HEAD after verifying its claims")
	fmt.Fprintln(writer, "  hook              emit agent injection or nudge output")
}

func runDreamRestamp(
	args []string,
	stdout, stderr io.Writer,
	runtime dreamCommandRuntime,
) int {
	flags := newFlagSet(
		"dream restamp",
		"usage: pfm dream restamp maps/{slug}.md   (run from inside the repository)",
		stderr,
	)
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "pfm dream restamp: resolve working directory: %v\n", err)
		return 1
	}
	output, err := runtime.restamp(flags.Arg(0), workingDirectory, runtime.now())
	if err != nil {
		fmt.Fprintf(stderr, "pfm dream restamp: %v\n", err)
		return 1
	}
	if _, writeErr := io.WriteString(stdout, output); writeErr != nil {
		fmt.Fprintf(stderr, "pfm dream restamp: write stdout: %v\n", writeErr)
		return 1
	}
	return 0
}
