package update

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// updateConfigMigrationTestRuntime is updateRollbackTestRuntime, plus a
// pre-split legacy config.json outside the source clone and a runtime whose
// Config.Path names it. PFM_CONFIG pins that external file while the
// candidate's own `install --yes` migrates it to pfm.config.json.
func updateConfigMigrationTestRuntime(
	t *testing.T,
) (runtime pfmconfig.Runtime, repo, legacyPath, migratedPath string, originalContent []byte) {
	t.Helper()
	runtime, repo = updateRollbackTestRuntime(t)
	configDir := t.TempDir()
	legacyPath = filepath.Join(configDir, pfmconfig.LegacyFileName)
	migratedPath = filepath.Join(configDir, pfmconfig.FileName)
	t.Setenv(paths.EnvConfig, legacyPath)
	originalContent = []byte(`{"version":2,"theme":"tokyo-night"}`)
	if err := os.WriteFile(legacyPath, originalContent, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime.Config = pfmconfig.Config{Path: legacyPath, Exists: true}
	return runtime, repo, legacyPath, migratedPath, originalContent
}

// TestUpdateCandidateDoctorReceivesTheMigratedConfigPath is a REGRESSION
// test for issue #24 finding 4 (M3 change C): the candidate's own
// `install --yes` renames config.json -> pfm.config.json INSIDE the
// candidate process only, so the updater's runtime.Config.Path (resolved
// before the install ran) names a file that no longer exists by the time the
// post-install doctor runs. Unfixed, the gating doctor is handed the stale
// legacy path and judges the host entirely on defaults.
func TestUpdateCandidateDoctorReceivesTheMigratedConfigPath(t *testing.T) {
	runtime, repo, legacyPath, migratedPath, originalContent := updateConfigMigrationTestRuntime(t)

	oldBuild, oldInstall, oldRunDoctor := updateBuildCandidate, updateApplyInstall, updateRunDoctor
	t.Cleanup(func() {
		updateBuildCandidate, updateApplyInstall, updateRunDoctor = oldBuild, oldInstall, oldRunDoctor
	})
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		return testjail.WriteExecutable(output, []byte("new\n"), 0o755)
	}
	updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		// Simulates the candidate's install migrating the pre-split config.
		return os.Rename(legacyPath, migratedPath)
	}
	var capturedConfigPath string
	updateRunDoctor = func(_ context.Context, _ string, _ pfmconfig.Runtime, configPath string, _ bool, _, _ io.Writer) (doctorOutcome, error) {
		capturedConfigPath = configPath
		return doctorOutcome{}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--repo", repo}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("Run() code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if capturedConfigPath != migratedPath {
		t.Fatalf("candidate doctor --config=%q, want the migrated path %q", capturedConfigPath, migratedPath)
	}
	if !strings.Contains(stdout.String(), "config migrated by the update: "+legacyPath+" → "+migratedPath) {
		t.Fatalf("stdout=%q, want the config-migrated note", stdout.String())
	}
	if got, err := os.ReadFile(migratedPath); err != nil || !bytes.Equal(got, originalContent) {
		t.Fatalf(
			"migrated config=%q err=%v, want the original bytes %q untouched by this test",
			got,
			err,
			originalContent,
		)
	}
}

// TestUpdateConfigPathAfterInstallSurfacesANonENOENTStatError is a
// REGRESSION test for issue #24 F2: updateConfigPathAfterInstall used to
// treat ANY os.Stat error on the original config path — not only
// fs.ErrNotExist — as "gone", falling through to the migrated-path probe
// and then to the "config is gone" note, silently handing the candidate
// doctor an empty configPath. A non-ENOENT stat error (here: the config's
// parent directory loses execute permission, so the kernel refuses to even
// traverse into it) must surface as an error the caller treats as a failed
// update step, never as a path handoff.
func TestUpdateConfigPathAfterInstallSurfacesANonENOENTStatError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	runtime := updateTestRuntime(t)
	parent := filepath.Join(t.TempDir(), "locked")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(parent, pfmconfig.FileName)
	if err := os.WriteFile(original, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(parent, 0o700); err != nil {
			t.Errorf("restore parent permissions: %v", err)
		}
	})
	runtime.Config = pfmconfig.Config{Path: original, Exists: true}

	path, note, err := updateConfigPathAfterInstall(runtime)
	if err == nil {
		t.Fatalf(
			"updateConfigPathAfterInstall(...) = (%q, %q, nil), want a non-nil error for a non-ENOENT stat failure",
			path,
			note,
		)
	}
	if !strings.Contains(err.Error(), original) {
		t.Fatalf("error=%v, want it to name the path %q", err, original)
	}
}
