package installer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type missingGitOutputRunner struct{}

func (missingGitOutputRunner) Run(context.Context, string, ...string) error {
	return errors.New("Run not scripted")
}

func (missingGitOutputRunner) Output(context.Context, string, ...string) ([]byte, error) {
	return nil, &exec.Error{Name: "git", Err: exec.ErrNotFound}
}

var (
	_ CommandRunner = missingGitOutputRunner{}
	_ OutputRunner  = missingGitOutputRunner{}
)

// TestReportSourceRepoMarkerPresentReportsOK pins the "marker present" branch:
// an existing source-repo marker is reported ok, naming the kept repo, and
// never silently skipped.
func TestReportSourceRepoMarkerPresentReportsOK(t *testing.T) {
	home := t.TempDir()
	if err := WriteSourceRepoMarker(home, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	installer := &engine{options: Options{Home: home, Stdout: &stdout}}
	if err := installer.reportSourceRepoMarker(); err != nil {
		t.Fatalf("reportSourceRepoMarker() error = %v", err)
	}
	if installer.report.OK != 1 {
		t.Fatalf("report.OK = %d, want 1", installer.report.OK)
	}
	if installer.report.Skipped != 0 {
		t.Fatalf("report.Skipped = %d, want 0", installer.report.Skipped)
	}
	if !strings.Contains(stdout.String(), "ok") || !strings.Contains(stdout.String(), "kept:") {
		t.Fatalf("stdout = %q, want an ok line naming the kept repo", stdout.String())
	}
}

// TestReportSourceRepoMarkerAbsentReportsNamedSkip is a REGRESSION test for
// the install-with-no-SourceRepo path: a run with no --repo used to skip the
// marker check silently (writeUpdateMetadata's `else if` branch did not
// exist), leaving pfm init/update readers of the marker with no diagnostic
// at all when it was missing. reportSourceRepoMarker must instead render
// that absence as a NAMED skip line, never as if nothing were expected there.
func TestReportSourceRepoMarkerAbsentReportsNamedSkip(t *testing.T) {
	home := t.TempDir()
	var stdout bytes.Buffer
	installer := &engine{options: Options{Home: home, Stdout: &stdout}}
	if err := installer.reportSourceRepoMarker(); err != nil {
		t.Fatalf("reportSourceRepoMarker() error = %v", err)
	}
	if installer.report.Skipped != 1 {
		t.Fatalf("report.Skipped = %d, want 1", installer.report.Skipped)
	}
	if installer.report.OK != 0 {
		t.Fatalf("report.OK = %d, want 0", installer.report.OK)
	}
	want := "source repository not found — run pfm install from inside your Professor clone or set PFM_SOURCE_REPO; pfm init and pfm update read it"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want the named skip diagnostic %q", stdout.String(), want)
	}
}

// TestReportSourceRepoMarkerOtherErrorIsReturnedNeverSkipped pins the third
// branch: a marker that exists but fails to read for a reason other than
// absence (here, a directory in its place, so os.ReadFile itself errors)
// must be RETURNED as a real error, never folded into the same skip line an
// absent marker gets — an error is never "nothing there".
func TestReportSourceRepoMarkerOtherErrorIsReturnedNeverSkipped(t *testing.T) {
	home := t.TempDir()
	markerPath := SourceRepoPath(home)
	// A directory in the marker's place fails os.ReadFile with something
	// other than fs.ErrNotExist (EISDIR), the shape reportSourceRepoMarker
	// must return rather than skip.
	if err := os.MkdirAll(markerPath, 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	installer := &engine{options: Options{Home: home, Stdout: &stdout}}
	err := installer.reportSourceRepoMarker()
	if err == nil {
		t.Fatal("reportSourceRepoMarker() error = nil, want a read failure")
	}
	if strings.Contains(err.Error(), "not found") {
		t.Fatalf("reportSourceRepoMarker() folded a real error into the absence message: %v", err)
	}
	if installer.report.Skipped != 0 || installer.report.OK != 0 {
		t.Fatalf("report = %+v, want neither ok nor skip recorded for a real error", installer.report)
	}
}

// TestWriteUpdateMetadataWithNoSourceRepoReportsTheMarkerSkip is a REGRESSION
// test for writeUpdateMetadata's own call site: an install run with no
// --repo/SourceRepo (Options.SourceRepo == "") used to fall straight through
// to the binary-ownership write and never call reportSourceRepoMarker at
// all, so a missing source-repo marker produced NO line about it whatsoever
// — silent, not even a skip. The fix adds the `else if
// installer.reportSourceRepoMarker()` branch; this pins that writeUpdateMetadata
// itself (not just the helper in isolation) reports the named skip.
func TestWriteUpdateMetadataWithNoSourceRepoReportsTheMarkerSkip(t *testing.T) {
	home := t.TempDir()
	var stdout bytes.Buffer
	installer := &engine{options: Options{Home: home, Stdout: &stdout}, apply: true}
	if err := installer.writeUpdateMetadata(); err != nil {
		t.Fatalf("writeUpdateMetadata() error = %v", err)
	}
	want := "source repository not found — run pfm install from inside your Professor clone or set PFM_SOURCE_REPO; pfm init and pfm update read it"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want the named skip diagnostic from writeUpdateMetadata", stdout.String())
	}
}

// TestInstallSkipsPrePushGateWhenGitIsUnavailable is a REGRESSION test for
// the install-time pre-push probe. A missing git binary is a graceful skip,
// preserving install's documented behavior, even though the runner wraps the
// process lookup failure in exec.ErrNotFound.
func TestInstallSkipsPrePushGateWhenGitIsUnavailable(t *testing.T) {
	home := t.TempDir()
	clone := t.TempDir()
	hooks := filepath.Join(clone, ".githooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-push"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	installer := &engine{options: Options{
		Home:       home,
		SourceRepo: clone,
		Stdout:     &stdout,
		Runner:     missingGitOutputRunner{},
	}, apply: true}
	if err := installer.writeUpdateMetadata(); err != nil {
		t.Fatalf("writeUpdateMetadata() error = %v, want graceful git-unavailable skip", err)
	}
	want := "git unavailable — pre-push gate not armed in " + clone
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want named skip %q", stdout.String(), want)
	}
}
