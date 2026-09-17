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

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/store"
	"hostops/pfm/internal/testjail"
)

// buildCleanDoctorHome stages the fixture a healthy target HOME carries —
// the canonical binary, the Claude launcher, both host overlays, and every
// PFM_* jail env var — and returns the runtime a clean `pfm doctor` run
// reads. Shared by TestDoctorFreshTargetHomeIsClean and every M2 tier test
// that needs a clean baseline to add exactly one defect on top of.
func buildCleanDoctorHome(t *testing.T) commandRuntime {
	t.Helper()
	clearRetiredHarvesterEnv(t) // golden doctor output must not depend on an ambient retired harvester variable
	runtime := testjail.CleanHome(t)
	stageHarnessPromptBaseline(t, runtime.Paths.Home)
	return runtime
}

func TestDoctorFreshTargetHomeIsClean(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	var stdout, stderr bytes.Buffer
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("fresh target HOME doctor code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "doctor: clean") {
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
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
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
	if err := os.WriteFile(filepath.Join(canonicalDir, "pfm"), []byte("target-pfm"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostShimDir, "pfm"), []byte("host-pfm"), 0o700); err != nil {
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
	if err := os.WriteFile(filepath.Join(canonicalDir, "pfm"), []byte("canonical-pfm"), 0o700); err != nil {
		t.Fatal(err)
	}
	hostShim := filepath.Join(hostShimDir, "pfm")
	if err := os.WriteFile(hostShim, []byte("shadowing-pfm"), 0o700); err != nil {
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
