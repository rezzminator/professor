// Package themecmd is the `pfm theme` verb (status, ls, set, sync, off) and the
// name-sync convergence door of the theme: it parses the verb, hands the work
// to themesync, and draws the sync matrix the user looks at — one row per
// surface with its verdict named, never a bare exit code.
package themecmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/themesync"
	"github.com/rezzminator/professor/pfm/internal/themetmux"
)

const (
	verbStatus = "status"
	verbLs     = "ls"
	verbSet    = "set"
	verbSync   = "sync"
	verbOff    = "off"
)

const usage = `usage: pfm theme [status|ls|set|sync|off] [options]
  status             the sync matrix: one row per surface, its verdict and generation (default; --json)
  ls                 every palette with its dark and light swatches; the current one is marked *
  set <palette>      theme every surface now and keep them in sync (--appearance auto|dark|light, --json)
  sync               follow the system appearance now (--force re-applies; --json)
  off                restore every surface exactly as it was and stop syncing (--json)`

// Ops is themesync as the verb calls it; tests inject fakes.
type Ops struct {
	Set           func(context.Context, themesync.Env, themesync.Request) (themesync.Report, error)
	Sync          func(ctx context.Context, env themesync.Env, force bool) (themesync.Report, error)
	ReadBack      func(context.Context, themesync.Env) (themesync.Report, error)
	Off           func(context.Context, themesync.Env) (themesync.Report, error)
	Palettes      func() []themesync.Palette
	PaletteByName func(string) (themesync.Palette, error)
	SyncOn        func(stateDir string) bool
}

// Deps is everything a run touches.
type Deps struct {
	Ops Ops
	// Env builds the themesync environment; an error is a failed run.
	Env func() (themesync.Env, error)
	// Color is whether stdout takes truecolor escapes.
	Color bool
}

// RealOps is themesync itself.
func RealOps() Ops {
	return Ops{
		Set: themesync.Set, Sync: themesync.Sync, ReadBack: themesync.ReadBack, Off: themesync.Off,
		Palettes: themesync.Palettes, PaletteByName: themesync.PaletteByName, SyncOn: themesync.SyncOn,
	}
}

// pfmLauncher is the installed pfm the appearance follower's launch agent
// runs: the stable launcher every launch-time hook names, never a versioned
// or test binary.
func pfmLauncher(home string) string { return filepath.Join(home, ".local", "bin", "pfm") }

// launchdJobVariable is the variable launchd sets to a job's label in every
// process it starts for the job.
const launchdJobVariable = "XPC_SERVICE_NAME"

// surfaceEnv is the themesync environment over the real runner and pfm's
// tmux servers, naming the launchd job this process runs as.
func surfaceEnv(values paths.Values, env paths.Env) (themesync.Env, error) {
	if values.Home == "" {
		return themesync.Env{}, fmt.Errorf("no home directory resolved")
	}
	return themesync.Env{
		Home:       values.Home,
		StateDir:   themesync.DefaultStateDir(values.Home),
		PfmBinary:  pfmLauncher(values.Home),
		Runner:     obs.Runner(deps.RealRunner{}),
		Tmux:       themetmux.AtDir(values.TmuxDir),
		LaunchdJob: env.Get(launchdJobVariable),
	}, nil
}

// Main runs `pfm theme` against the real home, tmux directory and terminal.
func Main(args []string, stdout, stderr io.Writer, values paths.Values) int {
	file, isFile := stdout.(interface{ Fd() uintptr })
	env := paths.OSEnv{}
	color := isFile && term.IsTerminal(file.Fd()) && env.Get("NO_COLOR") == "" && env.Get("TERM") != "dumb"
	return RunVerb(context.Background(), args, Deps{
		Ops:   RealOps(),
		Color: color,
		Env:   func() (themesync.Env, error) { return surfaceEnv(values, env) },
	}, stdout, stderr)
}

// RunVerb is the verb: args are those after `theme`; the result is the exit code
// (0 done, 1 a surface out of sync or a run failed, 2 usage).
func RunVerb(ctx context.Context, args []string, d Deps, stdout, stderr io.Writer) int {
	verb := verbStatus
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, args = args[0], args[1:]
	}
	run := runner{ctx: ctx, deps: d, stdout: stdout, stderr: stderr}
	switch verb {
	case verbStatus, verbLs, verbSync, verbOff:
		return run.flagged(verb, args)
	case verbSet:
		return run.set(args)
	case "help":
		fmt.Fprintln(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "pfm theme: unknown subcommand %q\n%s\n", verb, usage)
	return 2
}

type runner struct {
	ctx    context.Context
	deps   Deps
	stdout io.Writer
	stderr io.Writer
}

func (run runner) fail(err error) int {
	fmt.Fprintf(run.stderr, "pfm theme: %v\n", err)
	return 1
}

// flagged runs the verbs whose only options are --json and (sync) --force.
func (run runner) flagged(verb string, args []string) int {
	flags := cli.NewFlagSet("pfm theme "+verb, usage, run.stderr)
	asJSON := flags.Bool("json", false, "print the report as JSON")
	force := new(bool)
	if verb == verbSync {
		force = flags.Bool("force", false, "re-apply even when the appearance is unchanged")
	}
	positional, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		if code == 0 {
			fmt.Fprintln(run.stdout, usage)
		}
		return code
	}
	if len(positional) != 0 {
		fmt.Fprintf(run.stderr, "pfm theme %s: unexpected argument %q\n%s\n", verb, positional[0], usage)
		return 2
	}
	env, err := run.deps.Env()
	if err != nil {
		return run.fail(err)
	}
	if verb == verbLs {
		return run.ls(env)
	}
	// A sync that was never switched on has nothing to read, follow or
	// re-apply: say so, never fail. (off still runs: it restores whatever a
	// half-finished earlier run left.)
	if verb != verbOff && !run.deps.Ops.SyncOn(env.StateDir) {
		return run.printReport(themesync.Report{}, *asJSON)
	}
	var report themesync.Report
	switch verb {
	case verbStatus:
		report, err = run.deps.Ops.ReadBack(run.ctx, env)
	case verbSync:
		report, err = run.deps.Ops.Sync(run.ctx, env, *force)
	default:
		report, err = run.deps.Ops.Off(run.ctx, env)
	}
	if err != nil {
		return run.fail(err)
	}
	return run.printReport(report, *asJSON)
}

func (run runner) set(args []string) int {
	flags := cli.NewFlagSet("pfm theme set", usage, run.stderr)
	asJSON := flags.Bool("json", false, "print the report as JSON")
	appearance := flags.String("appearance", string(themesync.ModeAuto), "auto, dark or light")
	positional, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		if code == 0 {
			fmt.Fprintln(run.stdout, usage)
		}
		return code
	}
	if len(positional) != 1 {
		fmt.Fprintf(run.stderr, "usage: pfm theme set <palette> [--appearance auto|dark|light] [--json]\n")
		return 2
	}
	mode := themesync.Mode(*appearance)
	if mode != themesync.ModeAuto && mode != themesync.ModeDark && mode != themesync.ModeLight {
		fmt.Fprintf(run.stderr, "pfm theme set: appearance %q is not one of auto, dark or light\n", *appearance)
		return 2
	}
	if _, err := run.deps.Ops.PaletteByName(positional[0]); err != nil {
		names := make([]string, 0, 8)
		palettes := run.deps.Ops.Palettes()
		for i := range palettes {
			names = append(names, palettes[i].Name)
		}
		if len(names) == 0 {
			return run.fail(err)
		}
		fmt.Fprintf(
			run.stderr,
			"pfm theme set: unknown palette %q — known: %s\n",
			positional[0],
			strings.Join(names, ", "),
		)
		return 2
	}
	env, err := run.deps.Env()
	if err != nil {
		return run.fail(err)
	}
	report, err := run.deps.Ops.Set(run.ctx, env, themesync.Request{Palette: positional[0], Mode: mode})
	if err != nil {
		return run.fail(err)
	}
	return run.printReport(report, *asJSON)
}

// printReport draws one report and answers the exit code its verdicts earn.
func (run runner) printReport(report themesync.Report, asJSON bool) int {
	code := 0
	if !report.OK() {
		code = 1
	}
	if asJSON {
		if report.Surfaces == nil {
			report.Surfaces = []themesync.SurfaceResult{}
		}
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return run.fail(fmt.Errorf("encode the report: %w", err))
		}
		fmt.Fprintf(run.stdout, "%s\n", encoded)
		return code
	}
	drawMatrix(run.stdout, report, run.deps.Color)
	return code
}
