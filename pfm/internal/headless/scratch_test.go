package headless

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// TestPreparedScratchDirReturnsInjectedDirectoryUnchanged pins the pass-
// through branch: every existing test in this package (and every real
// chat-status call carrying options.TempDir) supplies a directory directly,
// and preparedScratchDir must hand it back untouched rather than second-
// guessing it.
func TestPreparedScratchDirReturnsInjectedDirectoryUnchanged(t *testing.T) {
	const injected = "/some/injected/directory"
	got, err := preparedScratchDir(injected)
	if err != nil || got != injected {
		t.Fatalf("preparedScratchDir(%q) = (%q, %v), want (%q, nil)", injected, got, err, injected)
	}
}

// scratchJail mirrors summaryTestStore's jail idiom (paths.EnvHome plus
// paths.EnvSIDDir via t.Setenv) for the tests below that drive
// preparedScratchDir's empty-directory branch: paths.Resolve()'s SIDDir
// defaults to /tmp/cc-sid — the real crumb pfm/CLAUDE.md forbids any test
// from touching — so the override is not optional here.
func scratchJail(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv(paths.EnvHome, filepath.Join(root, "home"))
	sidDir := filepath.Join(root, "sid")
	t.Setenv(paths.EnvSIDDir, sidDir)
	return sidDir
}

// chdir moves the process working directory to directory for the rest of the
// test and restores it after, so a test can assert nothing lands relative to
// "wherever the operator happened to be standing."
func chdir(t *testing.T, directory string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatalf("chdir %s: %v", directory, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore cwd %s: %v", previous, err)
		}
	})
}

// TestWritePreparedExchangeEmptyDirectoryResolvesThroughPathsNotCWD is the
// regression test: an empty directory must resolve through internal/paths
// (here, the jailed PFM_SID_DIR) rather than CWD-relative
// filepath.Join("tmp", "chat-status"). It drives the exact branch the dev
// agent reported as unexercised by the rest of the suite — every other test
// in this package injects a non-empty TempDir.
func TestWritePreparedExchangeEmptyDirectoryResolvesThroughPathsNotCWD(t *testing.T) {
	sidDir := scratchJail(t)
	cwd := t.TempDir()
	chdir(t, cwd)

	prompt := []transcript.Entry{{Role: transcript.RoleUser, Text: "what changed?"}}
	response := []transcript.Entry{{Role: transcript.RoleAssistant, Text: "the cache changed"}}
	path, err := writePreparedExchange("", prompt, response, true)
	if err != nil {
		t.Fatalf("writePreparedExchange(empty directory): %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	if !strings.HasPrefix(path, sidDir+string(filepath.Separator)) {
		t.Fatalf("prepared exchange landed at %q, want under the jailed SID directory %q", path, sidDir)
	}
	if _, statErr := os.Stat(filepath.Join(cwd, "tmp")); !os.IsNotExist(statErr) {
		t.Fatalf("a tmp/ directory appeared in the working directory %s: stat err=%v", cwd, statErr)
	}
}

// TestWritePreparedCaptureEmptyDirectoryResolvesThroughPathsNotCWD is the
// same regression pin for statusask.go's writePreparedCapture.
func TestWritePreparedCaptureEmptyDirectoryResolvesThroughPathsNotCWD(t *testing.T) {
	sidDir := scratchJail(t)
	cwd := t.TempDir()
	chdir(t, cwd)

	chat := Chat{Name: "seat"}
	path, err := writePreparedCapture("", chat, "live pane content")
	if err != nil {
		t.Fatalf("writePreparedCapture(empty directory): %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	if !strings.HasPrefix(path, sidDir+string(filepath.Separator)) {
		t.Fatalf("prepared capture landed at %q, want under the jailed SID directory %q", path, sidDir)
	}
	if _, statErr := os.Stat(filepath.Join(cwd, "tmp")); !os.IsNotExist(statErr) {
		t.Fatalf("a tmp/ directory appeared in the working directory %s: stat err=%v", cwd, statErr)
	}
}

// TestPreparedScratchDirResolutionFailureIsAnErrorNotAGuess pins that a
// paths.Resolve() failure comes back as an error rather than a silent
// fallback. paths.Resolve() itself refuses to resolve the operator's real
// home while running under `go test` unless PFM_HOME (or the explicit
// PFM_TEST_REAL_HOME opt-out) is set — see paths.go's EnvRealHome comment —
// so clearing both here forces the seam without inventing a new one.
func TestPreparedScratchDirResolutionFailureIsAnErrorNotAGuess(t *testing.T) {
	t.Setenv(paths.EnvHome, "")
	t.Setenv(paths.EnvRealHome, "")
	cwd := t.TempDir()
	chdir(t, cwd)

	dir, err := preparedScratchDir("")
	if err == nil {
		t.Fatalf("preparedScratchDir(empty) = (%q, nil), want a resolution error", dir)
	}
	if !strings.Contains(err.Error(), "resolve scratch directory") {
		t.Fatalf("preparedScratchDir error = %q, want it to name the resolve-scratch-directory step", err.Error())
	}

	prompt := []transcript.Entry{{Role: transcript.RoleUser, Text: "what changed?"}}
	response := []transcript.Entry{{Role: transcript.RoleAssistant, Text: "the cache changed"}}
	if path, writeErr := writePreparedExchange("", prompt, response, true); writeErr == nil {
		t.Fatalf("writePreparedExchange(empty directory) = (%q, nil), want the resolution error propagated", path)
	}
	if _, statErr := os.Stat(filepath.Join(cwd, "tmp")); !os.IsNotExist(statErr) {
		t.Fatalf(
			"a tmp/ directory appeared in the working directory %s despite the resolution failure: stat err=%v",
			cwd,
			statErr,
		)
	}
}
