// Package themecmd is the `pfm theme` verb (status, sync, on, off, list, use, check) and the
// name-sync convergence door of the theme: it parses the verb, hands the work
// to themesync, and draws the sync matrix the user looks at — the Claude
// source and one row per surface with its verdict named, never a bare exit
// code. `list` and `use` are the one place the user picks a theme: use sets
// the `theme` of every Claude account, then one sync repaints the surfaces.
// Under the follower's launchd job a sync prints only what it changed or what
// failed.
package themecmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/term"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pyvalue"
	"github.com/rezzminator/professor/pfm/internal/themesync"
	"github.com/rezzminator/professor/pfm/internal/themetmux"
)

const (
	verbStatus = "status"
	verbSync   = "sync"
	verbOn     = "on"
	verbOff    = "off"
	verbList   = "list"
	verbUse    = "use"
	verbCheck  = "check"
)

const usage = `usage: pfm theme [status|sync|on|off|list|use|check] [options]
  status             the sync matrix: the Claude source, one row per surface, its verdict and generation (default; --json)
  sync               follow Claude Code's theme now (--force re-applies; --json)
  on                 follow Claude Code's theme: install the follower and theme every surface (--json)
  off                restore every surface exactly as it was and stop syncing (--json)
  list               the themes use accepts, * marking the current one (--json)
  use {name}         set the theme of every Claude account, then sync; new chats follow (--json)
  check [file...]    run install's profile checks, render each VS Code theme and hold its text to the
                     contrast floors, writing nothing; TextMate scope names are not checked against any
                     grammar; no file: every profile in the source clone's templates/themes/ (--json)`

// Ops is themesync as the verb calls it; tests inject fakes.
type Ops struct {
	On       func(context.Context, themesync.Env) (themesync.Report, error)
	Sync     func(ctx context.Context, env themesync.Env, force bool) (themesync.Report, error)
	ReadBack func(context.Context, themesync.Env) (themesync.Report, error)
	Off      func(context.Context, themesync.Env) (themesync.Report, error)
	SyncOn   func(stateDir string) bool
	LogOnce  func(env themesync.Env, line string) (fresh bool, err error)
	List     func(themesync.Env) (themesync.ThemeList, error)
	Use      func(env themesync.Env, name string) (themesync.UseResult, error)
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
		On: themesync.On, Sync: themesync.Sync, ReadBack: themesync.ReadBack, Off: themesync.Off,
		SyncOn: themesync.SyncOn, LogOnce: themesync.LogOnce,
		List: themesync.ListThemes,
		Use: func(env themesync.Env, name string) (themesync.UseResult, error) {
			return themesync.UseTheme(env, name, installer.EditSettings)
		},
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
// (0 done, 1 a surface out of sync, an account refused or a run failed, 2 usage).
func RunVerb(ctx context.Context, args []string, d Deps, stdout, stderr io.Writer) int {
	verb := verbStatus
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, args = args[0], args[1:]
	}
	run := runner{ctx: ctx, deps: d, stdout: stdout, stderr: stderr}
	switch verb {
	case verbStatus, verbSync, verbOn, verbOff:
		return run.flagged(verb, args)
	case verbList:
		return run.list(args)
	case verbUse:
		return run.use(args)
	case verbCheck:
		return run.check(args)
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

// command is one verb's command line: its flags and positionals.
type command struct {
	asJSON, force bool
	positional    []string
}

// parse reads a verb's --json and (sync) --force around its positionals;
// ok false means the exit code is already decided (help 0, a bad flag 2).
func (run runner) parse(verb string, args []string) (cmd command, code int, ok bool) {
	flags := cli.NewFlagSet("pfm theme "+verb, usage, run.stderr)
	asJSON := flags.Bool("json", false, "print the result as JSON")
	force := new(bool)
	if verb == verbSync {
		force = flags.Bool("force", false, "re-apply even when the appearance is unchanged")
	}
	positional, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		if code == 0 {
			fmt.Fprintln(run.stdout, usage)
		}
		return command{}, code, false
	}
	return command{asJSON: *asJSON, force: *force, positional: positional}, 0, true
}

// unexpected is the usage exit of a verb handed an argument it does not take.
func (run runner) unexpected(verb, arg string) int {
	fmt.Fprintf(run.stderr, "pfm theme %s: unexpected argument %q\n%s\n", verb, arg, usage)
	return 2
}

// flagged runs the verbs whose only options are --json and (sync) --force.
func (run runner) flagged(verb string, args []string) int {
	cmd, code, ok := run.parse(verb, args)
	if !ok {
		return code
	}
	if len(cmd.positional) != 0 {
		return run.unexpected(verb, cmd.positional[0])
	}
	env, err := run.deps.Env()
	if err != nil {
		return run.fail(err)
	}
	quiet := verb == verbSync && env.LaunchdJob == themesync.FollowerLabel && !cmd.asJSON
	// A sync that was never switched on has nothing to read, follow or
	// re-apply: say so, never fail. (off still runs: it restores whatever a
	// half-finished earlier run left; on turns it on.)
	if verb != verbOff && verb != verbOn && !run.deps.Ops.SyncOn(env.StateDir) {
		if quiet {
			return 0
		}
		return run.printReport(themesync.Report{}, cmd.asJSON)
	}
	var report themesync.Report
	switch verb {
	case verbStatus:
		report, err = run.deps.Ops.ReadBack(run.ctx, env)
	case verbSync:
		report, err = run.deps.Ops.Sync(run.ctx, env, cmd.force)
	case verbOn:
		report, err = run.deps.Ops.On(run.ctx, env)
	default:
		report, err = run.deps.Ops.Off(run.ctx, env)
	}
	if quiet {
		return run.quiet(env, report, err)
	}
	if err != nil {
		return run.fail(err)
	}
	return run.printReport(report, cmd.asJSON)
}

// quiet is the follower's sync: nothing printed when nothing changed and
// every surface reads back; one line per apply; one line per failure, the
// same failure twice printed once (themesync.LogOnce keeps the last line).
func (run runner) quiet(env themesync.Env, report themesync.Report, runErr error) int {
	line, failed := "", false
	switch {
	case runErr != nil:
		line, failed = "theme sync: "+pyvalue.OneLine(runErr.Error()), true
	case !report.OK():
		line = fmt.Sprintf("theme sync gen %d failed: %s", report.Generation, badSurfaces(report))
		failed = true
	case report.Changed && report.Source != nil:
		line = fmt.Sprintf("theme sync gen %d: %s (%s) from %s · tmux %d · herdr · vscode contract",
			report.Generation, report.Source.Value, report.Appearance, report.Source.Path, report.Servers)
	}
	record := ""
	if failed {
		record = line
	}
	fresh, err := run.deps.Ops.LogOnce(env, record)
	if err != nil {
		fmt.Fprintf(run.stderr, "pfm theme: record the follower log line: %v\n", err)
		return 1
	}
	switch {
	case failed && fresh:
		fmt.Fprintln(run.stderr, line)
	case !failed && line != "":
		fmt.Fprintln(run.stdout, line)
	}
	if failed {
		return 1
	}
	return 0
}

// badSurfaces names every surface of a report in DRIFT or ERROR with its
// detail, one line.
func badSurfaces(report themesync.Report) string {
	var bad []string
	for _, surface := range report.Surfaces {
		if surface.Verdict == themesync.VerdictDrift || surface.Verdict == themesync.VerdictError {
			bad = append(bad, surface.Surface+" "+string(surface.Verdict)+" "+pyvalue.OneLine(surface.Detail))
		}
	}
	return strings.Join(bad, "; ")
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

// printJSON writes value as indented JSON on stdout; a failure to encode is a
// failed run.
func (run runner) printJSON(value any) bool {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		run.fail(fmt.Errorf("encode the result: %w", err))
		return false
	}
	fmt.Fprintf(run.stdout, "%s\n", encoded)
	return true
}

// list is `pfm theme list`: the themes use accepts, read whether the sync is
// on or off. Exit 1 when a themes dir or settings file could not be read.
func (run runner) list(args []string) int {
	cmd, code, ok := run.parse(verbList, args)
	if !ok {
		return code
	}
	if len(cmd.positional) != 0 {
		return run.unexpected(verbList, cmd.positional[0])
	}
	env, err := run.deps.Env()
	if err != nil {
		return run.fail(err)
	}
	list, err := run.deps.Ops.List(env)
	if err != nil {
		return run.fail(err)
	}
	if list.Themes == nil {
		list.Themes = []themesync.ThemeChoice{}
	}
	if list.Errors == nil {
		list.Errors = []themesync.ThemeListError{}
	}
	code = 0
	if len(list.Errors) > 0 {
		code = 1
	}
	if cmd.asJSON {
		if !run.printJSON(list) {
			return 1
		}
		return code
	}
	drawList(run.stdout, list)
	return code
}

// drawList draws `NAME  VALUE  TITLE`, a `*` ahead of the current theme and
// `missing in {dirs}` after the title of a custom theme some account lacks,
// then an ERROR row per read that failed.
func drawList(out io.Writer, list themesync.ThemeList) {
	nameWidth, valueWidth, titleWidth := utf8.RuneCountInString("NAME"), utf8.RuneCountInString("VALUE"), 0
	for _, choice := range list.Themes {
		nameWidth = max(nameWidth, utf8.RuneCountInString(choice.Typed()))
		valueWidth = max(valueWidth, utf8.RuneCountInString(choice.Value))
		titleWidth = max(titleWidth, utf8.RuneCountInString(choice.Title))
	}
	row := func(mark, name, value, title, note string) {
		line := fmt.Sprintf("%s %-*s  %-*s  %-*s", mark, nameWidth, name, valueWidth, value, titleWidth, title)
		if note != "" {
			line += "  " + note
		}
		fmt.Fprintln(out, strings.TrimRight(line, " "))
	}
	row(" ", "NAME", "VALUE", "TITLE", "")
	for _, choice := range list.Themes {
		mark, note := " ", ""
		if choice.Current {
			mark = "*"
		}
		if len(choice.Missing) > 0 {
			note = "missing in " + strings.Join(choice.Missing, ", ")
		}
		row(mark, choice.Typed(), choice.Value, choice.Title, note)
	}
	for _, failure := range list.Errors {
		fmt.Fprintf(out, "ERROR %s: %s\n", failure.Path, pyvalue.OneLine(failure.Error))
	}
}

// useOutcome is `pfm theme use` as --json prints it.
type useOutcome struct {
	Use    themesync.UseResult `json:"use"`
	Report *themesync.Report   `json:"report"`
}

// use is `pfm theme use {name}`: set the theme of every Claude account, then
// one sync when the sync is on. Exit 0 only when every account was written and
// the sync reports OK; every cause of exit 1 is named on stderr, which is what
// the VS Code extension shows.
func (run runner) use(args []string) int {
	cmd, code, ok := run.parse(verbUse, args)
	if !ok {
		return code
	}
	switch {
	case len(cmd.positional) == 0:
		fmt.Fprintf(run.stderr, "pfm theme use: needs exactly one theme name (use {name})\n%s\n", usage)
		return 2
	case len(cmd.positional) > 1:
		return run.unexpected(verbUse, cmd.positional[1])
	}
	env, err := run.deps.Env()
	if err != nil {
		return run.fail(err)
	}
	result, useErr := run.deps.Ops.Use(env, cmd.positional[0])
	if result.Written == nil {
		result.Written = []string{}
	}
	if result.Refused == nil {
		result.Refused = []themesync.ThemeListError{}
	}
	code = 0
	for _, refusal := range result.Refused {
		fmt.Fprintf(run.stderr, "refused %s: %s\n", refusal.Path, pyvalue.OneLine(refusal.Error))
		code = 1
	}
	if !cmd.asJSON {
		for _, path := range result.Written {
			fmt.Fprintf(run.stdout, "wrote %s\n", path)
		}
	}
	if useErr != nil {
		if cmd.asJSON && result.Value != "" && !run.printJSON(useOutcome{Use: result}) {
			return 1
		}
		return run.fail(useErr)
	}
	var report *themesync.Report
	syncOn := run.deps.Ops.SyncOn(env.StateDir)
	if syncOn {
		synced, err := run.deps.Ops.Sync(run.ctx, env, false)
		if err != nil {
			fmt.Fprintf(run.stderr, "pfm theme: sync: %v\n", err)
			code = 1
		} else {
			report = &synced
			if !synced.OK() {
				fmt.Fprintf(run.stderr, "pfm theme: sync left surfaces out of sync: %s\n", badSurfaces(synced))
				code = 1
			}
		}
	}
	if cmd.asJSON {
		if !run.printJSON(useOutcome{Use: result, Report: report}) {
			return 1
		}
		return code
	}
	switch {
	case report != nil:
		drawMatrix(run.stdout, *report, run.deps.Color)
	case !syncOn:
		fmt.Fprintln(run.stdout, "theme sync is off: run pfm theme on to repaint tmux, herdr and VS Code")
	}
	fmt.Fprintln(run.stdout, "new chats follow; run /theme in open chats")
	return code
}
