package doctor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/nudge"
)

func TestDoctorCrumbHealthAcceptsNudgeMetadataAndRejectsAnEmptyIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := nudge.RecordContext(dir, "session-a", 45); err != nil {
		t.Fatal(err)
	}
	if _, _, err := nudge.Decide(dir, "session-a", 45, 35, 10); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nudge-ctx-"), []byte("45\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, invalid, err := crumbHealth(dir)
	if err != nil || entries != 3 || invalid != 1 {
		t.Fatalf(
			"entries=%d invalid=%d err=%v; legitimate sample/band must pass, empty identity must fail",
			entries,
			invalid,
			err,
		)
	}
}

// TestDoctorVerboseWritesUnderSIDDirNotCWDTmp is 1-c's regression test:
// unfixed, doctor's --verbose dir is the cwd-relative "tmp/pfm-doctor" — this
// test's cwd is a fresh temp dir with no PFM_SID_DIR relationship, so the
// printed line and the dir handed to the dependency probe / harness capture
// diverge from it, and a stray tmp/ appears in the cwd.
func TestDoctorVerboseWritesUnderSIDDirNotCWDTmp(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	wantDir := filepath.Join(runtime.Paths.SIDDir, "pfm-doctor")

	savedProbe := DependencyProbeOverride
	savedCapture := HarnessCaptureOverride
	t.Cleanup(func() {
		DependencyProbeOverride = savedProbe
		HarnessCaptureOverride = savedCapture
	})
	var gotProbeDir, gotCaptureDir string
	DependencyProbeOverride = func(_ context.Context, entries []deps.Entry, options deps.ProbeOptions) []deps.Result {
		gotProbeDir = options.VerboseDir
		return savedProbe(context.Background(), entries, options)
	}
	HarnessCaptureOverride = func(
		ctx context.Context, home string, machine config.Config, alias, verboseDir string,
	) (HarnessCapture, error) {
		gotCaptureDir = verboseDir
		return savedCapture(ctx, home, machine, alias, verboseDir)
	}

	cwd := t.TempDir()
	t.Chdir(cwd)

	var stdout, stderr bytes.Buffer
	runDoctor([]string{"--verbose"}, &stdout, &stderr, runtime)

	wantLine := fmt.Sprintf("doctor: verbose output dir=%s\n", wantDir)
	if !strings.Contains(stdout.String(), wantLine) {
		t.Fatalf("stdout missing %q:\n%s", wantLine, stdout.String())
	}
	if gotProbeDir != wantDir {
		t.Fatalf("dependency probe VerboseDir=%q, want %q", gotProbeDir, wantDir)
	}
	if gotCaptureDir != wantDir {
		t.Fatalf("harness capture verboseDir=%q, want %q", gotCaptureDir, wantDir)
	}
	if _, err := os.Stat(filepath.Join(cwd, "tmp")); !os.IsNotExist(err) {
		t.Fatalf("doctor --verbose left a cwd-relative tmp/: err=%v", err)
	}
}

// TestDoctorWithoutVerboseFlagPrintsNoDirLine is 1-c's negative case: no
// --verbose, no info line, no directory created.
func TestDoctorWithoutVerboseFlagPrintsNoDirLine(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	cwd := t.TempDir()
	t.Chdir(cwd)

	var stdout, stderr bytes.Buffer
	runDoctor(nil, &stdout, &stderr, runtime)

	if strings.Contains(stdout.String(), "doctor: verbose output dir") {
		t.Fatalf("doctor without --verbose printed a verbose dir line:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(runtime.Paths.SIDDir, "pfm-doctor")); !os.IsNotExist(err) {
		t.Fatalf("doctor without --verbose created its verbose dir: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "tmp")); !os.IsNotExist(err) {
		t.Fatalf("doctor without --verbose left a cwd-relative tmp/: err=%v", err)
	}
}
