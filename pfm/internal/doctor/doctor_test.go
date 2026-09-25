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

// TestPrintDependenciesCountsAnUnknownProbeStateLikeMissing: a probe state
// PrintDependencies does not know is an unverified dependency — a required
// engine dependency in it is a failure (so `pfm install` preflight refuses),
// an optional or harvest one a warning; the row itself is unchanged.
func TestPrintDependenciesCountsAnUnknownProbeStateLikeMissing(t *testing.T) {
	saved := DependencyProbeOverride
	t.Cleanup(func() { DependencyProbeOverride = saved })
	DependencyProbeOverride = func(_ context.Context, entries []deps.Entry, _ deps.ProbeOptions) []deps.Result {
		results := make([]deps.Result, 0, len(entries))
		for _, entry := range entries {
			results = append(results, deps.Result{Entry: entry, State: deps.State("wedged")})
		}
		return results
	}
	cases := []struct {
		name                   string
		entry                  deps.Entry
		wantWarning, wantFails int
	}{
		{name: "required engine dep", entry: deps.Entry{Name: "tmux", Required: true}, wantFails: 1},
		{name: "required harvest dep", entry: deps.Entry{Name: "uv", Required: true, Harvest: true}, wantWarning: 1},
		{name: "optional dep", entry: deps.Entry{Name: "jq"}, wantWarning: 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout bytes.Buffer
			warnings, failures, _ := PrintDependencies(
				context.Background(), &stdout, t.TempDir(), []deps.Entry{testCase.entry}, deps.ProbeOptions{},
			)
			if warnings != testCase.wantWarning || failures != testCase.wantFails {
				t.Fatalf("warnings=%d failures=%d, want %d/%d:\n%s",
					warnings, failures, testCase.wantWarning, testCase.wantFails, stdout.String())
			}
			want := fmt.Sprintf("doctor: dep %s broken error=unknown probe state %q\n", testCase.entry.Name, "wedged")
			if stdout.String() != want {
				t.Fatalf("row = %q, want %q", stdout.String(), want)
			}
		})
	}
}
