package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// The tracer-intake check proves the launch hook can work on this host: python3
// resolves, the kit file the hook names is readable, and the rendered hook
// command, run as Claude Code runs it, passes a non-tracer call through and
// opens a run for a tracer spawn (docs/design/hooks/hooks.md § The pfm doctor
// checks). Its rows name the hook as the registry does.
const (
	tracerIntakeRow = "doctor: tracer-intake:"
	// tracerScratchPrefix names the temporary repo root the tracer smoke
	// creates; the kit derives its run's /tmp/{project} from that name, so the
	// prefix is what makes the cleanup's removal safe.
	tracerScratchPrefix = "pfm-doctor-tracer-"
	// tracerPythonName is the dependency registry row the check resolves python3
	// through.
	tracerPythonName = "python3"
	// kitScratchRoot is where the tracetree kit writes its runs, a literal of
	// the kit itself (tracetree.py open_run).
	kitScratchRoot = "/tmp"

	// tracerFixPython is the consequence of a python3 failure; the install step
	// is the registry row's InstallHint, which the doctor: dep python3 row prints.
	tracerFixPython = "the tracer cannot run without python3, a required dependency (the kit uses only the Python 3 " +
		"standard library and states no minimum version): install it as the doctor: dep python3 row says, and put it " +
		"on the PATH Claude is launched with"
	tracerFixKit = "run pfm install --yes to relink the tracetree skill"
	// tracerFixRelink serves every smoke failure: a kit that did not run is a
	// broken link or file, a missing interpreter, or a hook that hung.
	tracerFixRelink = "run pfm install --yes to relink the kit; " + tracerFixPython
)

// tracerIntakeBound caps every subprocess the check starts; a variable so a
// test can prove the timeout row in a second.
var tracerIntakeBound = 20 * time.Second

var tracerRunLine = regexp.MustCompile(`(?m)^RUN: (\S+)\s*$`)

// kitRootSafe is an absolute path the kit's brief parser (tracetree.py
// INTAKE_ROOT, which stops at Python's Unicode whitespace, a quote, a bracket,
// a comma or a semicolon) reads whole: a temporary root outside it would be
// read truncated, open no run and fail a healthy host.
var kitRootSafe = regexp.MustCompile(`^/[A-Za-z0-9/._+@%=~-]*$`)

// printTracerIntakeDoctor prints the tracer-intake rows and returns their
// failure count. The command and the kit come from the claudelaunch registry's
// tracer-intake row. The launch hands its hooks the PATH of whatever process
// launched Claude (pfm sets none), which doctor cannot see: python3 is judged
// on doctor's own PATH, and the row says so.
//
// A kit the install has not linked yet is a non-failing row: `pfm install --yes`
// brings it. The installer records no individual skill link; its
// ~/.local/share/pfm/install/skill-links.json names the directories it links
// skills into. So "placed" is read from the link at the hook's path: its
// directory absent is not installed yet, unless that record shows the kit
// linked in another directory (a FAIL: the tracer's hook reads only the
// default config dir) or cannot be read (a FAIL); present with the kit file
// unreadable or dangling is a failure.
func printTracerIntakeDoctor(
	ctx context.Context,
	stdout io.Writer,
	home string,
	dependencies Dependencies,
) (failures int) {
	hook, kit, found := claudelaunch.TracerIntakeHook(home)
	if !found {
		fmt.Fprintf(stdout, "%s FAIL need=hook checked=claudelaunch.HookTemplates error=%q — fix: this pfm build "+
			"carries no tracer-intake hook to prove; rebuild pfm from its source\n", tracerIntakeRow, "no tracer-intake row")
		return 1
	}
	fail := func(need, checked, key, detail, fix string) {
		failures++
		fmt.Fprintf(
			stdout,
			"%s FAIL need=%s checked=%s %s=%q — fix: %s\n",
			tracerIntakeRow,
			need,
			checked,
			key,
			detail,
			fix,
		)
	}
	runner := dependencies.Runner
	environment := deps.EnvironmentWith("PATH", dependencies.Env.Get("PATH"))

	python, version, pythonOK := checkTracerPython(ctx, runner, home, fail)
	pending, kitOK := checkTracerKit(home, kit, fail)
	if pending {
		fmt.Fprintf(stdout, "%s kit not installed yet — run pfm install --yes\n", tracerIntakeRow)
		return failures
	}
	if !pythonOK || !kitOK {
		fail(
			"smoke",
			hook.Command,
			"error",
			"not run: python3 or the kit failed above",
			"fix the rows above, then rerun pfm doctor",
		)
		return failures
	}
	smokeFailures, cleanupErr := runTracerSmokes(ctx, runner, environment, hook.Command, fail)
	if cleanupErr != nil {
		fail(
			"cleanup",
			kitScratchRoot,
			"error",
			cleanupErr.Error(),
			"remove the leftover path by hand; the smoke created it",
		)
	}
	if smokeFailures == 0 && cleanupErr == nil {
		fmt.Fprintf(stdout, "%s ok python3=%s version=%s kit=%s smoke passed\n", tracerIntakeRow, python, version, kit)
	}
	return failures
}

type tracerFailer func(need, checked, key, detail, fix string)

// checkTracerPython judges python3 through its deps.Registry row, the one
// place that names the command, how it is resolved and how its version is
// read: the same deps.Probe the doctor: dep python3 row runs, under this
// check's own tracerIntakeBound where the dep row takes the probe's default
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
		LookPath: runner.LookPath, Runner: runner, Timeout: tracerIntakeBound,
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

// checkTracerKit judges the kit file the hook names. pending is true when the
// install has not linked the kit anywhere yet.
func checkTracerKit(home, kit string, fail tracerFailer) (pending, ok bool) {
	if _, err := os.Lstat(filepath.Dir(kit)); errors.Is(err, fs.ErrNotExist) {
		return kitNotLinkedAtHook(home, kit, fail)
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

// kitNotLinkedAtHook decides what an absent kit directory at the hook's path
// means, from the installer's skill-links.json: the directories it links
// skills into. No file is no install, so the kit is pending. A file that cannot
// be read is a FAIL. The kit linked in a listed directory other than the hook's
// is a FAIL naming both paths: the install put it where the tracer's hook does
// not read, and pfm install --yes will not move it. Linked nowhere is pending.
func kitNotLinkedAtHook(home, kit string, fail tracerFailer) (pending, ok bool) {
	ledger := filepath.Join(installer.ManagedRoot(home), "skill-links.json")
	dirs, recorded, err := installer.SkillLinkDirs(home)
	if err != nil {
		fail("kit", ledger, "error", err.Error(), "repair or delete "+ledger+", then run pfm install --yes")
		return false, false
	}
	if !recorded {
		return true, false
	}
	hookDir := filepath.Dir(filepath.Dir(kit))
	for _, dir := range dirs {
		if filepath.Clean(dir) == hookDir {
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
			"the install linked the tracetree kit at "+linked+", but the tracer and its hook read "+
				filepath.Dir(kit)+"; the tracer cannot run on this host",
			"pfm install --yes does not fix this: link the kit at the hook's path, or install without --config-dir",
		)
		return false, false
	}
	return true, false
}

// runTracerSmokes runs the rendered hook command twice through sh -c, as
// Claude Code runs it: a non-tracer call must pass through silently, a tracer
// spawn must come back rewritten with a RUN: line. Everything the smokes
// create is removed before it returns; cleanupErr says what could not be.
func runTracerSmokes(
	ctx context.Context, runner deps.Runner, environment []string, command string, fail tracerFailer,
) (failures int, cleanupErr error) {
	parent := os.TempDir()
	if !kitRootSafe.MatchString(parent) {
		// The kit writes its run under kitScratchRoot anyway, so the smoke
		// already needs it writable.
		parent = kitScratchRoot
	}
	root, err := os.MkdirTemp(parent, tracerScratchPrefix)
	if err != nil {
		fail(
			"smoke",
			"temporary directory",
			"error",
			err.Error(),
			"make the temporary directory writable, then rerun pfm doctor",
		)
		return 1, nil
	}
	var run string
	defer func() {
		cleanupErr = removeTracerSmoke(root, run)
	}()
	// The kit names the run's /tmp/{project} after the git repository the root
	// sits in: no inherited GIT_DIR and no checkout above the root may name it,
	// so the run stays under the root's own /tmp/{base}, which cleanup removes.
	environment = append(deps.WithoutGitRepoVars(environment), "GIT_CEILING_DIRECTORIES="+filepath.Dir(root))
	failed := func(need, detail, key, fix string) {
		failures++
		fail(need, command, key, detail, fix)
	}

	passthrough, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "tool_name": "Bash", "cwd": root,
		"tool_input": map[string]any{"command": "true"},
	})
	result, ran := runTracerCommand(ctx, runner, environment, command, root, passthrough, "smoke-passthrough", failed)
	if ran {
		if out := strings.TrimSpace(string(result.Stdout)); out != "" {
			failed("smoke-passthrough", "stdout not empty: "+capturedLine(result.Stdout, ""), "error", tracerFixRelink)
		}
	}

	spawn, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "tool_name": "Agent", "cwd": root,
		"tool_input": map[string]any{
			"subagent_type": "tracer",
			"description":   "pfm doctor smoke",
			"prompt":        "Repo root: " + root + "\nGoal: prove the intake hook opens a run\n1. What is in this directory?\n",
		},
	})
	result, ran = runTracerCommand(ctx, runner, environment, command, root, spawn, "smoke-tracer", failed)
	if !ran {
		return failures, nil
	}
	var output struct {
		Hook struct {
			Updated struct {
				Prompt string `json:"prompt"`
			} `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(result.Stdout, &output); err != nil {
		failed("smoke-tracer", "stdout is not JSON: "+err.Error()+": "+capturedLine(result.Stdout, "empty"), "error",
			tracerFixRelink)
		return failures, nil
	}
	match := tracerRunLine.FindStringSubmatch(output.Hook.Updated.Prompt)
	if match == nil {
		failed(
			"smoke-tracer",
			"updatedInput prompt carries no RUN: line: "+capturedLine(result.Stdout, "empty"),
			"error",
			"the kit opened no run: check that "+kitScratchRoot+" is writable, then run pfm install --yes",
		)
		return failures, nil
	}
	run = match[1]
	if info, err := os.Stat(run); err != nil || !info.IsDir() {
		detail := "not a directory"
		if err != nil {
			detail = err.Error()
		}
		failed("smoke-tracer", "run directory "+run+": "+detail, "error",
			"check that "+kitScratchRoot+" is writable, then run pfm install --yes")
	}
	return failures, nil
}

// runTracerCommand runs command through sh -c with payload on stdin, bounded.
// ran is false, after a FAIL row, when the command could not run or exited
// non-zero.
func runTracerCommand(
	ctx context.Context,
	runner deps.Runner,
	environment []string,
	command, dir string,
	payload []byte,
	need string,
	failed func(need, detail, key, fix string),
) (result deps.RunResult, ran bool) {
	bounded, cancel := context.WithTimeout(ctx, tracerIntakeBound)
	defer cancel()
	result, err := runner.Run(bounded, []string{"/bin/sh", "-c", command}, deps.RunOptions{
		Env: environment, Dir: dir, Stdin: payload, WaitDelay: 2 * time.Second, ProcessGroup: true,
	})
	switch {
	case bounded.Err() != nil:
		failed(need, fmt.Sprintf("timed out after %s", tracerIntakeBound), "error", tracerFixRelink)
	case err != nil:
		failed(need, "could not run /bin/sh -c: "+err.Error(), "error", "install a POSIX sh at /bin/sh")
	case result.ExitCode != 0:
		failed(need, capturedLine(result.Stderr, fmt.Sprintf("exit %d", result.ExitCode)), "stderr", tracerFixRelink)
	default:
		return result, true
	}
	return result, false
}

// removeTracerSmoke removes everything the smokes created: the run directory
// the kit opened, the kit's /tmp/{project} for the temporary root (named by the
// root's own unique base, so a real project's runs are never touched), and the
// root itself.
func removeTracerSmoke(root, run string) error {
	var problems []error
	base := filepath.Base(root)
	if run != "" && filepath.Base(filepath.Dir(run)) == "tracer" {
		problems = append(problems, os.RemoveAll(run))
		// The project directory is the RUN line's own, never a literal; it is
		// removed only when the kit named it for this temporary root.
		if project := filepath.Dir(filepath.Dir(run)); filepath.Base(project) == base {
			problems = append(problems, os.RemoveAll(project))
		}
	}
	if strings.HasPrefix(base, tracerScratchPrefix) {
		// No RUN line to follow (a failed smoke): the kit may still have logged
		// under its own /tmp literal.
		problems = append(problems, os.RemoveAll(filepath.Join(kitScratchRoot, base)))
	}
	problems = append(problems, os.RemoveAll(root))
	return errors.Join(problems...)
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
