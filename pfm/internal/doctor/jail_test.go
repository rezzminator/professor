package doctor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	goRuntime "runtime"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestDoctorJailRecordsCheckoutForPromptReaders(t *testing.T) {
	dirs, files := storeLayout()
	runtime := testjail.CleanHome(t, dirs, files)
	clone, err := paths.ReadSourceRepoMarker(runtime.Paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := paths.ComposedHarnessPrompt(runtime.Paths.Home, engine.Claude)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(prompt); err != nil {
		t.Fatalf("clone %s prompt %s: %v", clone, prompt, err)
	}
	dir, err := paths.HarnessBaselineDir(runtime.Paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(filepath.Join(dir, "harness-original.sha256")); err != nil {
		t.Fatal(err)
	}
}

// buildCleanDoctorHome stages the fixture a healthy target HOME carries —
// the canonical binary, the Claude launcher, both host overlays, and every
// PFM_* jail env var — and returns the runtime a clean `pfm doctor` run
// reads. Shared by TestDoctorFreshTargetHomeIsClean and every M2 tier test
// that needs a clean baseline to add exactly one defect on top of.
func buildCleanDoctorHome(t *testing.T) commandRuntime {
	t.Helper()
	clearRetiredHarvesterEnv(t) // golden doctor output must not depend on an ambient retired harvester variable
	dirs, files := storeLayout()
	runtime := testjail.CleanHome(t, dirs, files)
	stageStorePlugins(t, runtime.Paths.Home)
	return runtime
}

func TestDoctorFreshTargetHomeIsClean(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	var stdout, stderr bytes.Buffer
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("fresh target HOME doctor code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	// claude_plugins ok proves doctor judged the store the plugins live in
	// (installer.ClaudeStore), not a dir with no settings.json that only
	// ever prints the skipped line.
	if !strings.Contains(stdout.String(), "doctor: clean") ||
		!strings.Contains(stdout.String(), "doctor: claude_plugins ok\n") ||
		!strings.Contains(stdout.String(), "host-check: ok (21 checks)") ||
		!strings.Contains(stdout.String(), "account-links: ok (1 accounts × 23 entries)") {
		t.Fatalf("fresh target HOME doctor output=%q", stdout.String())
	}
}

// TestDoctorReportsClaudeVersionCountBytesAndPrunable is M7's doctor-row
// regression test for issue #24 finding 8: the launcher disables Claude
// Code's own version cleanup, so nothing else in pfm ever reported the
// growth. Three versions on a clean target HOME: the newest is protected,
// the middle one is live (a PFM_PROC_ROOT fixture pid's exe points at it),
// and the oldest is the only one prunable. Unfixed, doctor prints no
// claude-versions row at all — this test's count=/prunable= assertions fail.
func TestDoctorReportsClaudeVersionCountBytesAndPrunable(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	home := runtime.Paths.Home
	versions := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	newest := filepath.Join(versions, "2.1.270")
	live := filepath.Join(versions, "2.1.263")
	prunable := filepath.Join(versions, "2.1.250")
	for _, path := range []string{newest, live, prunable} {
		if err := testjail.WriteExecutable(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	procRoot := runtime.Paths.ProcRoot
	if err := os.MkdirAll(filepath.Join(procRoot, "4242"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(live, filepath.Join(procRoot, "4242", "exe")); err != nil {
		t.Fatal(err)
	}
	// Candidate scoping (installer.ProbeLiveClaudeVersions) reads argv[0]
	// before ever calling Image, so the fixture needs a cmdline record
	// naming the live build.
	if err := os.WriteFile(filepath.Join(procRoot, "4242", "cmdline"), []byte(live+"\x00"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	runDoctor(nil, &stdout, &stderr, runtime)
	output := stdout.String()
	if !strings.Contains(output, "doctor: claude-versions dir="+versions) {
		t.Fatalf("doctor output missing claude-versions row:\n%s", output)
	}
	if !strings.Contains(output, "count=3") {
		t.Fatalf("doctor output missing count=3:\n%s", output)
	}
	if !strings.Contains(output, "newest=2.1.270") {
		t.Fatalf("doctor output missing newest=2.1.270:\n%s", output)
	}
	if !strings.Contains(output, "live=2.1.263(1 pids)") {
		t.Fatalf("doctor output missing live pid count:\n%s", output)
	}
	if !strings.Contains(output, "prunable=1") {
		t.Fatalf("doctor output missing prunable=1:\n%s", output)
	}
}

// TestDoctorExitsThreeOnARequiredDependencyMissingAndOneOnWarningsAlone is
// M2's regression test for issue #24 finding 1: doctor must distinguish a
// FAILURE (a required dependency missing) from a WARNING (an advisory row
// no install step owns), gate the exit code on failures alone, and print
// `doctor: failures=` only when a failure exists. Unfixed, both cases exit
// 1 — the second assertion (exit 3, `doctor: failures=1`) fails against the
// unfixed code.
func TestDoctorExitsThreeOnARequiredDependencyMissingAndOneOnWarningsAlone(t *testing.T) {
	t.Run("a warning-tier row alone exits 1 with no failures line", func(t *testing.T) {
		runtime := buildCleanDoctorHome(t)
		database, err := store.Open(store.WithWarningWriter(io.Discard))
		if err != nil {
			t.Fatal(err)
		}
		// busy_kill_warnings is an advisory meta counter (doctor.go's own table:
		// "everything else ... busy counters" stays a warning) — the row this
		// case adds carries no failure.
		if err := database.SetMeta(context.Background(), "busy_kill_warnings", "1"); err != nil {
			if closeErr := database.Close(); closeErr != nil {
				t.Fatalf("set busy kill warning: %v; close database: %v", err, closeErr)
			}
			t.Fatal(err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}

		var stdout, stderr bytes.Buffer
		code := runDoctor(nil, &stdout, &stderr, runtime)
		if code != 1 {
			t.Fatalf("warning-only doctor code=%d, want 1\nstdout=%s", code, stdout.String())
		}
		if strings.Contains(stdout.String(), "doctor: failures=") {
			t.Fatalf("warning-only doctor printed a failures= line:\n%s", stdout.String())
		}
		if !strings.Contains(stdout.String(), "doctor: warnings=") {
			t.Fatalf("warning-only doctor never printed its warnings= line:\n%s", stdout.String())
		}
	})

	t.Run("a required dependency missing exits 3 with failures=1", func(t *testing.T) {
		runtime := buildCleanDoctorHome(t)
		saved := DependencyProbeOverride
		t.Cleanup(func() { DependencyProbeOverride = saved })
		DependencyProbeOverride = func(_ context.Context, entries []deps.Entry, _ deps.ProbeOptions) []deps.Result {
			results := make([]deps.Result, 0, len(entries))
			for _, entry := range entries {
				if !entry.AppliesTo(goRuntime.GOOS) {
					results = append(
						results,
						deps.Result{Entry: entry, State: deps.StateSkipped, Error: "not this platform"},
					)
					continue
				}
				if entry.Name == "tmux" {
					results = append(results, deps.Result{Entry: entry, State: deps.StateMissing})
					continue
				}
				results = append(
					results,
					deps.Result{
						Entry:   entry,
						State:   deps.StateOK,
						Path:    "/test/bin/" + entry.Name,
						Version: entry.MinVersion,
					},
				)
			}
			return results
		}

		var stdout, stderr bytes.Buffer
		code := runDoctor(nil, &stdout, &stderr, runtime)
		if code != 3 {
			t.Fatalf("required-dependency-missing doctor code=%d, want 3\nstdout=%s", code, stdout.String())
		}
		if !strings.Contains(stdout.String(), "doctor: failures=1") {
			t.Fatalf("required-dependency-missing doctor never printed failures=1:\n%s", stdout.String())
		}
	})
}

// TestDoctorNamesTheOrphanedKillsItCounts pins the law "every check names what
// its own broken state reports": an orphaned kill was counted into
// `doctor: warnings=N` while the only line mentioning it was the neutral
// `doctor: rows ... orphaned_killed=1` census — a warning no printed line
// called a defect, which reads to a host operator as a phantom count. Unfixed,
// doctor still exits 1 and still prints the census row, so only the
// `doctor: warning orphaned_killed=` assertion fails.
func TestDoctorNamesTheOrphanedKillsItCounts(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	database, err := store.Open(store.WithWarningWriter(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	// A kill whose id resolves to no transcript, rollout, or OpenCode session
	// is exactly store's definition of an orphaned kill (health.go).
	killErr := database.Kill(context.Background(), store.Killed{ID: "orphan-kill-fixture", KilledAt: 1})
	if closeErr := database.Close(); closeErr != nil {
		t.Fatalf("kill fixture chat: %v; close database: %v", killErr, closeErr)
	}
	if killErr != nil {
		t.Fatal(killErr)
	}

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, &stdout, &stderr, runtime)
	output := stdout.String()
	if code != 1 {
		t.Fatalf("orphaned-kill doctor code=%d, want 1\nstdout=%s", code, output)
	}
	if !strings.Contains(output, "orphaned_killed=1") {
		t.Fatalf("doctor output missing the orphaned_killed census count:\n%s", output)
	}
	if !strings.Contains(output, "doctor: warning orphaned_killed=1") {
		t.Fatalf("doctor counted an orphaned kill but no printed line named it as a warning:\n%s", output)
	}
	if !strings.Contains(output, "doctor: remediation: list them with `pfm archive --prune-orphans`") {
		t.Fatalf("doctor named the orphaned kill without its remediation:\n%s", output)
	}
	if !strings.Contains(output, "doctor: warnings=1") {
		t.Fatalf("orphaned-kill doctor warning tally is not 1:\n%s", output)
	}
}

func TestPFMPathWarningsIgnoreHostShimsOutsideTargetHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_DEV_FENCE", "")
	canonicalDir := filepath.Join(home, ".local", "bin")
	hostShimDir := filepath.Join(t.TempDir(), "host-bin")
	for _, directory := range []string{canonicalDir, hostShimDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := testjail.WriteExecutable(filepath.Join(canonicalDir, "pfm"), []byte("target-pfm"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(filepath.Join(hostShimDir, "pfm"), []byte("host-pfm"), 0o700); err != nil {
		t.Fatal(err)
	}

	warnings := pfmPathWarnings(
		home,
		canonicalDir+string(os.PathListSeparator)+hostShimDir,
	)
	if len(warnings) != 0 {
		t.Fatalf("target HOME PATH warnings=%q, want none for host shim", warnings)
	}
}

func TestPFMPathWarningsReportHostShimsOutsideHomeWithoutAJail(t *testing.T) {
	t.Setenv("PFM_HOME", "")
	t.Setenv("PFM_DEV_FENCE", "")
	home := t.TempDir()
	canonicalDir := filepath.Join(home, ".local", "bin")
	hostShimDir := filepath.Join(t.TempDir(), "host-bin")
	for _, directory := range []string{canonicalDir, hostShimDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := testjail.WriteExecutable(filepath.Join(canonicalDir, "pfm"), []byte("canonical-pfm"), 0o700); err != nil {
		t.Fatal(err)
	}
	hostShim := filepath.Join(hostShimDir, "pfm")
	if err := testjail.WriteExecutable(hostShim, []byte("shadowing-pfm"), 0o700); err != nil {
		t.Fatal(err)
	}

	warnings := pfmPathWarnings(
		home,
		canonicalDir+string(os.PathListSeparator)+hostShimDir,
	)
	if !strings.Contains(strings.Join(warnings, "\n"), hostShim) {
		t.Fatalf("PATH warnings=%q, want out-of-home shadow %q reported", warnings, hostShim)
	}
}

func TestCrumbHealthNonDirectoryRemainsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sid-file")
	if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := crumbHealth(path); err == nil {
		t.Fatal("crumbHealth returned nil for a non-directory probe target")
	}
}

func TestCrumbHealthPermissionDeniedRemainsAnError(t *testing.T) {
	path := t.TempDir()
	_, _, err := crumbHealthWith(
		path,
		os.Stat,
		func(string) ([]os.DirEntry, error) { return nil, os.ErrPermission },
	)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("crumbHealth permission error=%v, want permission denied", err)
	}
}

func TestCrumbHealthMissingDirectoryIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sid")
	entries, invalid, err := crumbHealth(path)
	if err != nil {
		t.Fatalf("crumbHealth missing directory error=%v, want nil", err)
	}
	if entries != 0 || invalid != 0 {
		t.Fatalf("crumbHealth missing directory entries=%d invalid=%d, want 0/0", entries, invalid)
	}
}

// clearRetiredHarvesterEnv blanks every variable doctor reports as a retired
// harvester setting, so a developer shell that still exports one cannot turn
// a golden "clean" doctor run into a warning.
func clearRetiredHarvesterEnv(t *testing.T) {
	t.Helper()
	for _, retired := range RetiredHarvesterEnv {
		t.Setenv(retired.Name, "")
	}
}

// TestDoctorUnopenableDatabaseIsAFailureRowNotTheEnd: a database doctor cannot
// open is a failure row and exit 3, and doctor reads on to the host checks —
// `pfm update` reads the failure only from the `doctor: failures=N` line.
func TestDoctorUnopenableDatabaseIsAFailureRowNotTheEnd(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Both resolutions doctor reads — the store's own and the runtime's — name the unopenable path.
	runtime.Paths.StateDB = filepath.Join(blocked, "pfm.db")
	t.Setenv(paths.EnvStateDB, runtime.Paths.StateDB)

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, &stdout, &stderr, runtime)
	output := stdout.String()
	if code != 3 {
		t.Fatalf("unopenable database doctor code=%d, want 3\nstdout=%s", code, output)
	}
	if !strings.Contains(output, "doctor: unhealthy database: ") {
		t.Fatalf("the unhealthy database row is missing:\n%s", output)
	}
	if !strings.Contains(output, "host-check: ") || !strings.Contains(output, "doctor: rows could not look: ") {
		t.Fatalf("doctor stopped at the database instead of reading on:\n%s", output)
	}
	if !strings.Contains(output, "\ndoctor: failures=") {
		t.Fatalf("doctor printed no failure count:\n%s", output)
	}
}

// TestDoctorUsesTheInjectedRunnerAndEnv: Run's Dependencies are the seams a
// caller injects — the dependency probe runs through Dependencies.Runner and
// the log-level rows read Dependencies.Env, never the host's.
func TestDoctorUsesTheInjectedRunnerAndEnv(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	saved := DependencyProbeOverride
	t.Cleanup(func() { DependencyProbeOverride = saved })
	injected := &deps.FakeRunner{}
	var probeRunner deps.Runner
	DependencyProbeOverride = func(ctx context.Context, entries []deps.Entry, options deps.ProbeOptions) []deps.Result {
		probeRunner = options.Runner
		return saved(ctx, entries, options)
	}
	values := map[string]string{}
	for _, pair := range os.Environ() {
		if name, value, ok := strings.Cut(pair, "="); ok {
			values[name] = value
		}
	}
	values[paths.EnvLogLevel] = "chatty"
	dependencies := testDependencies()
	dependencies.Runner = injected
	dependencies.Env = &paths.MapEnv{Values: values, HomeDir: runtime.Paths.Home}

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr, runtime, dependencies)
	if probeRunner != deps.Runner(injected) {
		t.Fatalf("the dependency probe ran through %T, not the injected runner", probeRunner)
	}
	if !strings.Contains(stdout.String(), "doctor: log control refused: "+paths.EnvLogLevel) {
		t.Fatalf("the log rows did not read the injected env:\n%s", stdout.String())
	}
	if code == 0 || strings.Contains(stdout.String(), "doctor: clean") {
		t.Fatalf("a refused override ended doctor clean (code=%d):\n%s", code, stdout.String())
	}
}

// homeSnapshot maps each path under root to its mode and, for a file, its
// bytes or, for a symlink, its target, so two snapshots compare byte for byte.
func homeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entry := info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			entry += " -> " + target
		case info.Mode().IsRegular():
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entry += " " + string(content)
		}
		snapshot[path] = entry
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return snapshot
}

// TestDoctorLeavesHomeByteIdenticalWithALoggedOutClaude: on a migrated host one
// plain pfm doctor ran a logged-out claude with the ambient env, which
// recreated ~/.claude/backups in the shared store plus $HOME/.claude.json; the
// host check then reported store-identity and install refused. Doctor's claude
// runs each get a throwaway home, so $HOME, the store included, is unchanged.
func TestDoctorLeavesHomeByteIdenticalWithALoggedOutClaude(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	home := runtime.Paths.Home
	// The real dependency probe, so its claude runs reach the fake.
	saved := DependencyProbeOverride
	t.Cleanup(func() { DependencyProbeOverride = saved })
	DependencyProbeOverride = nil
	// Production's SID dir is /tmp/cc-sid, outside HOME.
	sid := filepath.Join(t.TempDir(), "sid")
	t.Setenv(paths.EnvSIDDir, sid)
	scratch := t.TempDir()
	record := filepath.Join(scratch, "config-dirs.log")
	bin := filepath.Join(scratch, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteLoggedOutClaude(
		filepath.Join(bin, engine.MustLookup(engine.Claude).Binary), record, "",
	); err != nil {
		t.Fatal(err)
	}
	codexRecord := filepath.Join(scratch, "codex-homes.log")
	if err := testjail.WriteLoggedOutCodex(
		filepath.Join(bin, engine.MustLookup(engine.Codex).Binary), codexRecord, "",
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	before := homeSnapshot(t, home)
	var stdout, stderr bytes.Buffer
	runDoctor(nil, &stdout, &stderr, runtime)
	after := homeSnapshot(t, home)
	for path, entry := range after {
		if before[path] != entry {
			t.Errorf("doctor changed %s", path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			t.Errorf("doctor removed %s", path)
		}
	}
	if runs := testjail.AssertClaudeRanInThrowawayHomes(t, home, sid, record); runs == 0 {
		t.Errorf("doctor never ran the fake claude; stdout:\n%s", stdout.String())
	}
	if runs := testjail.AssertCodexRanInThrowawayHomes(t, home, sid, codexRecord); runs == 0 {
		t.Errorf("doctor never ran the fake codex; stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "account rows left to pfm: auth") {
		t.Errorf("the logged-out throwaway home's auth row was judged as the binary's:\n%s", stdout.String())
	}
}
