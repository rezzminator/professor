package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// The tracetree-kit check proves the tracer's kit can run on this host:
// python3 resolves, the kit file the install links under the default Claude
// config dir is readable, and the kit, run the way the tracer runs it
// (`python3 {kit}`) with no subcommand, prints its usage line.
const (
	tracetreeKitRow = "doctor: tracetree-kit:"
	// tracerPythonName is the dependency registry row the check resolves python3
	// through.
	tracerPythonName = "python3"
	// tracetreeUsage opens the line the kit prints when it is given no
	// subcommand (tracetree.py's usage failure).
	tracetreeUsage = "usage: tracetree.py"

	// tracerFixPython is the consequence of a python3 failure; the install step
	// is the registry row's InstallHint, which the doctor: dep python3 row prints.
	tracerFixPython = "the tracer cannot run without python3, a required dependency (the kit uses only the Python 3 " +
		"standard library and states no minimum version): install it as the doctor: dep python3 row says, and put it " +
		"on the PATH Claude is launched with"
	tracerFixKit = "run pfm install --yes to relink the tracetree skill"
	// tracerFixRelink serves every run failure: a kit that did not print its
	// usage is a broken link or file, a missing interpreter, or a kit that hung.
	tracerFixRelink = "run pfm install --yes to relink the kit; " + tracerFixPython
)

// tracetreeKitBound caps every subprocess the check starts; a variable so a
// test can prove the timeout row in a second.
var tracetreeKitBound = 20 * time.Second

// tracetreeKitPath is the kit file the tracer runs, under the home's default
// Claude config dir, where pfm install links the tracetree skill.
func tracetreeKitPath(home string) string {
	return filepath.Join(home, ".claude", "skills", "tracetree", "tracetree.py")
}

// printTracetreeKitDoctor prints the tracetree-kit rows and returns their
// failure count. python3 is judged on doctor's own PATH: a chat's tracer runs
// on the PATH Claude was launched with, which doctor cannot see.
//
// A kit the install has not linked yet is a non-failing row: `pfm install --yes`
// brings it. The installer records no individual skill link; its
// ~/.local/share/pfm/install/skill-links.json names the directories it links
// skills into. So "placed" is read from the link at the kit's path: its
// directory absent is not installed yet, unless that record shows the kit
// linked in another directory (a FAIL: the tracer reads only the default
// config dir) or cannot be read (a FAIL); present with the kit file
// unreadable or dangling is a failure.
func printTracetreeKitDoctor(
	ctx context.Context,
	stdout io.Writer,
	home string,
	dependencies Dependencies,
) (failures int) {
	kit := tracetreeKitPath(home)
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
	pending, kitOK := checkTracerKit(home, kit, fail)
	if pending {
		fmt.Fprintf(stdout, "%s kit not installed yet — run pfm install --yes\n", tracetreeKitRow)
		return failures
	}
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

// checkTracerKit judges the kit file. pending is true when the install has
// not linked the kit anywhere yet.
func checkTracerKit(home, kit string, fail tracerFailer) (pending, ok bool) {
	if _, err := os.Lstat(filepath.Dir(kit)); errors.Is(err, fs.ErrNotExist) {
		return kitNotLinkedAtPath(home, kit, fail)
	} else if err != nil {
		fail("kit", filepath.Dir(kit), "error", err.Error(), tracerFixKit)
		return false, false
	}
	info, err := os.Stat(kit)
	if err != nil {
		fail("kit", kit, "error", err.Error(), tracerFixKit)
		return false, false
	}
	if !info.Mode().IsRegular() {
		fail("kit", kit, "error", "not a regular file", tracerFixKit)
		return false, false
	}
	file, err := os.Open(kit)
	if err != nil {
		fail("kit", kit, "error", err.Error(), tracerFixKit)
		return false, false
	}
	if err := file.Close(); err != nil {
		fail("kit", kit, "error", err.Error(), tracerFixKit)
		return false, false
	}
	return false, true
}

// kitNotLinkedAtPath decides what an absent kit directory at the kit's path
// means, from the installer's skill-links.json: the directories it links
// skills into. No file is no install, so the kit is pending. A file that cannot
// be read is a FAIL. The kit linked in a listed directory other than the
// default config dir is a FAIL naming both paths: the install put it where the
// tracer does not read, and pfm install --yes will not move it. Linked nowhere
// is pending.
func kitNotLinkedAtPath(home, kit string, fail tracerFailer) (pending, ok bool) {
	ledger := filepath.Join(installer.ManagedRoot(home), "skill-links.json")
	dirs, recorded, err := installer.SkillLinkDirs(home)
	if err != nil {
		fail("kit", ledger, "error", err.Error(), "repair or delete "+ledger+", then run pfm install --yes")
		return false, false
	}
	if !recorded {
		return true, false
	}
	skillsDir := filepath.Dir(filepath.Dir(kit))
	for _, dir := range dirs {
		if filepath.Clean(dir) == skillsDir {
			continue
		}
		linked := filepath.Join(dir, filepath.Base(filepath.Dir(kit)))
		if _, err := os.Lstat(linked); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			fail("kit", linked, "error", err.Error(), tracerFixKit)
			return false, false
		}
		fail(
			"kit",
			kit,
			"error",
			"the install linked the tracetree kit at "+linked+", but the tracer reads "+
				filepath.Dir(kit)+"; the tracer cannot run on this host",
			"pfm install --yes does not fix this: link the kit at the tracer's path, or install without --config-dir",
		)
		return false, false
	}
	return true, false
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
