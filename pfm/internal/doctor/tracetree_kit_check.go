package doctor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudeplugin"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// The tracetree-kit check proves flights:tracer's kit can run on this host:
// python3 resolves, the kit file in the flights plugin folder Claude Code loads
// is readable, and the kit, run the way the tracer runs it (`python3 {kit}`)
// with no subcommand, prints its usage line.
const (
	tracetreeKitRow = "doctor: tracetree-kit:"
	// tracerPythonName is the dependency registry row the check resolves python3
	// through.
	tracerPythonName = "python3"
	// tracetreeUsage opens the line the kit prints when it is given no
	// subcommand (tracetree.py's usage failure).
	tracetreeUsage = "usage: tracetree.py"
	// tracerPlugin is the plugin that ships the tracer and its kit.
	tracerPlugin = "flights"

	// tracerFixPython is the consequence of a python3 failure; the install step
	// is the registry row's InstallHint, which the doctor: dep python3 row prints.
	tracerFixPython = "flights:tracer cannot run without python3, a required dependency (the kit uses only the " +
		"Python 3 standard library and states no minimum version): install it as the doctor: dep python3 row says, " +
		"and put it on the PATH Claude is launched with"
	tracerFixKit = "run pfm install --yes to refresh the flights plugin copy"
	// tracerFixRelink serves every run failure: a kit that did not print its
	// usage is a broken copy, a missing interpreter, or a kit that hung.
	tracerFixRelink = tracerFixKit + "; " + tracerFixPython
)

// tracetreeKitBound caps every subprocess the check starts; a variable so a
// test can prove the timeout row in a second.
var tracetreeKitBound = 20 * time.Second

// tracetreeKitPath is the kit file flights:tracer runs: skills/tracetree/
// tracetree.py in the folder Claude Code loads the flights plugin from, read
// from the shared Claude store's plugin records through claudeplugin.Roots,
// the door agentrole reads plugin roles through. enabled is false when the
// store enables no flights plugin; enabled with an empty kit is a plugin no
// record names a folder for. The first enabled key with a folder wins, as in
// agentrole.
func tracetreeKitPath(home string) (kit string, enabled bool, err error) {
	roots, err := claudeplugin.Roots(installer.ClaudeStore(home), tracerPlugin)
	if err != nil {
		return "", false, err
	}
	for _, root := range roots {
		if root.Dir != "" {
			return filepath.Join(root.Dir, "skills", "tracetree", "tracetree.py"), true, nil
		}
	}
	return "", len(roots) != 0, nil
}

// printTracetreeKitDoctor prints the tracetree-kit rows and returns their
// failure count. python3 is judged on doctor's own PATH: a chat's tracer runs
// on the PATH Claude was launched with, which doctor cannot see.
//
// A store that enables no flights plugin is a non-failing row: `pfm install
// --yes` brings it. Plugin records that cannot be read, an enabled plugin with
// no recorded folder, and a kit file absent or unreadable in that folder are
// failures.
func printTracetreeKitDoctor(
	ctx context.Context,
	stdout io.Writer,
	home string,
	dependencies Dependencies,
) (failures int) {
	fail := func(need, checked, key, detail, fix string) {
		failures++
		fmt.Fprintf(
			stdout,
			"%s FAIL need=%s checked=%s %s=%q — fix: %s\n",
			tracetreeKitRow,
			need,
			checked,
			key,
			detail,
			fix,
		)
	}
	python, version, pythonOK := checkTracerPython(ctx, dependencies.Runner, home, fail)
	kit, enabled, err := tracetreeKitPath(home)
	records := "the " + tracerPlugin + " plugin records in " + installer.ClaudeStore(home)
	switch {
	case err != nil:
		fail("kit", records, "error", err.Error(), "repair the file the error names, then run pfm install --yes")
		return failures
	case !enabled:
		fmt.Fprintf(stdout, "%s %s plugin not installed yet — run pfm install --yes\n", tracetreeKitRow, tracerPlugin)
		return failures
	case kit == "":
		fail("kit", records, "error", "the "+tracerPlugin+" plugin is enabled but no record names its folder",
			tracerFixKit)
		return failures
	}
	kitOK := checkTracerKit(kit, fail)
	if !pythonOK || !kitOK {
		fail("run", tracerPythonName+" "+kit, "error", "not run: python3 or the kit failed above",
			"fix the rows above, then rerun pfm doctor")
		return failures
	}
	environment := deps.EnvironmentWith("PATH", dependencies.Env.Get("PATH"))
	if runTracetreeKit(ctx, dependencies.Runner, environment, python, kit, fail) {
		fmt.Fprintf(stdout, "%s ok python3=%s version=%s kit=%s usage printed\n", tracetreeKitRow, python, version, kit)
	}
	return failures
}

type tracerFailer func(need, checked, key, detail, fix string)

// checkTracerPython judges python3 through its deps.Registry row, the one
// place that names the command, how it is resolved and how its version is
// read: the same deps.Probe the doctor: dep python3 row runs, under this
// check's own tracetreeKitBound where the dep row takes the probe's default
// bound. A FAIL here is the consequence of that row (the tracer cannot run),
// not a second lookup.
func checkTracerPython(
	ctx context.Context, runner deps.Runner, home string, fail tracerFailer,
) (path, version string, ok bool) {
	checked := "registry entry " + tracerPythonName + " on doctor's PATH (the launch passes Claude its caller's PATH, " +
		"which pfm does not set)"
	var entry deps.Entry
	registry := deps.Registry(deps.Options{Home: home})
	for index := range registry {
		if registry[index].Name == tracerPythonName {
			entry = registry[index]
		}
	}
	if entry.Name == "" {
		fail("python3", "deps.Registry", "error", "no python3 row in the dependency registry",
			"this pfm build carries no python3 dependency to check; rebuild pfm from its source")
		return "", "", false
	}
	result := deps.Probe(ctx, []deps.Entry{entry}, deps.ProbeOptions{
		LookPath: runner.LookPath, Runner: runner, Timeout: tracetreeKitBound,
	})[0]
	switch result.State {
	case deps.StateOK:
		return result.Path, result.Version, true
	case deps.StateMissing:
		fail("python3", checked, "error", tracerPythonName+" not found on doctor's PATH", tracerFixPython)
	case deps.StateSkipped:
		fail("python3", checked, "error", tracerPythonName+" is not checked on this platform: "+result.Error,
			tracerFixPython)
	default:
		// A lookup that failed past "not found" (a relative PATH entry's
		// exec.ErrDot) reaches here with no path and no version probe run.
		if result.Path != "" {
			checked = result.Path + " " + strings.Join(entry.VersionArgs, " ")
		}
		fail("python3", checked, "error", string(result.State)+": "+result.Error, tracerFixPython)
	}
	return result.Path, "", false
}

// checkTracerKit judges the kit file in the flights plugin folder: present,
// a regular file and openable.
func checkTracerKit(kit string, fail tracerFailer) (ok bool) {
	info, err := os.Stat(kit)
	if err != nil {
		fail("kit", kit, "error", err.Error(), tracerFixKit)
		return false
	}
	if !info.Mode().IsRegular() {
		fail("kit", kit, "error", "not a regular file", tracerFixKit)
		return false
	}
	file, err := os.Open(kit)
	if err != nil {
		fail("kit", kit, "error", err.Error(), tracerFixKit)
		return false
	}
	if err := file.Close(); err != nil {
		fail("kit", kit, "error", err.Error(), tracerFixKit)
		return false
	}
	return true
}

// runTracetreeKit runs `python3 {kit}` with no subcommand, bounded: a kit
// that imports prints its usage line and exits non-zero. ok is false, after a
// FAIL row, when the kit could not run, outran the bound or printed no usage.
func runTracetreeKit(
	ctx context.Context, runner deps.Runner, environment []string, python, kit string, fail tracerFailer,
) (ok bool) {
	checked := python + " " + kit
	bounded, cancel := context.WithTimeout(ctx, tracetreeKitBound)
	defer cancel()
	result, err := runner.Run(bounded, []string{python, kit}, deps.RunOptions{
		Env: environment, WaitDelay: 2 * time.Second, ProcessGroup: true,
	})
	output := strings.TrimSpace(string(result.Stdout) + "\n" + string(result.Stderr))
	switch {
	case bounded.Err() != nil:
		fail("run", checked, "error", fmt.Sprintf("timed out after %s", tracetreeKitBound), tracerFixRelink)
	case err != nil:
		fail("run", checked, "error", "could not run: "+err.Error(), tracerFixRelink)
	case !strings.Contains(output, tracetreeUsage):
		// A traceback names its error on its last line.
		last := output[strings.LastIndex(output, "\n")+1:]
		if last == "" {
			last = fmt.Sprintf("no output, exit %d", result.ExitCode)
		}
		fail("run", checked, "output", "printed no `"+tracetreeUsage+"` line: "+capturedLine([]byte(last), ""),
			tracerFixRelink)
	default:
		return true
	}
	return false
}

// capturedLine is the first non-empty line of captured output, bounded for a
// visible row, or fallback when there is none.
func capturedLine(output []byte, fallback string) string {
	line := deps.FirstLine(string(output))
	if line == "" {
		return fallback
	}
	if len(line) > 300 {
		line = line[:300] + "…"
	}
	return line
}
