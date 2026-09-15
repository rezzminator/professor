package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
)

// updateConfigMigrationTestRuntime is updateRollbackTestRuntime, plus a
// pre-split legacy config.json on disk and a runtime whose Config.Path names
// it — the shape a host has right before the candidate's own `install --yes`
// runs the v0.74.0 migration (config.json -> pfm.config.json) inside the
// candidate process only.
func updateConfigMigrationTestRuntime(
	t *testing.T,
) (runtime commandRuntime, repo, legacyPath, migratedPath string, originalContent []byte) {
	t.Helper()
	runtime, repo = updateRollbackTestRuntime(t)
	configDir := filepath.Join(runtime.Paths.Home, ".config", "pfm")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath = filepath.Join(configDir, pfmconfig.LegacyFileName)
	migratedPath = filepath.Join(configDir, pfmconfig.FileName)
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
		return os.WriteFile(output, []byte("new\n"), 0o755)
	}
	updateApplyInstall = func(context.Context, string, string, string, commandRuntime, bool, io.Writer, io.Writer) error {
		// Simulates the candidate's install migrating the pre-split config.
		return os.Rename(legacyPath, migratedPath)
	}
	var capturedConfigPath string
	updateRunDoctor = func(_ context.Context, _ string, _ commandRuntime, configPath string, _ bool, _, _ io.Writer) (doctorOutcome, error) {
		capturedConfigPath = configPath
		return doctorOutcome{}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})

	var stdout, stderr bytes.Buffer
	if code := runUpdate([]string{"--repo", repo}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("runUpdate() code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
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

// TestUpdateRollbackRestoresTheConfigFilesTheMigrationRenamed is a
// REGRESSION test for issue #24 finding 3 (M3 change D): when the candidate
// doctor fails and the update rolls back, the config files the candidate's
// own install renamed must be restored BEFORE the previous release's
// installer runs — otherwise that installer's `install --yes --config
// <legacy path>` finds nothing there, converges on defaults, and tears down
// the MCP launch agent it reads as "unconfigured". Unfixed: pfm.config.json
// remains and config.json stays absent after rollback.
func TestUpdateRollbackRestoresTheConfigFilesTheMigrationRenamed(t *testing.T) {
	runtime, repo, legacyPath, migratedPath, originalContent := updateConfigMigrationTestRuntime(t)

	oldBuild, oldInstall, oldRunDoctor := updateBuildCandidate, updateApplyInstall, updateRunDoctor
	oldRollbackInstall, oldRollbackDoctor := updateRollbackInstall, updateRollbackDoctor
	t.Cleanup(func() {
		updateBuildCandidate, updateApplyInstall, updateRunDoctor = oldBuild, oldInstall, oldRunDoctor
		updateRollbackInstall, updateRollbackDoctor = oldRollbackInstall, oldRollbackDoctor
	})
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		return os.WriteFile(output, []byte("new\n"), 0o755)
	}
	updateApplyInstall = func(context.Context, string, string, string, commandRuntime, bool, io.Writer, io.Writer) error {
		return os.Rename(legacyPath, migratedPath)
	}
	updateRunDoctor = func(context.Context, string, commandRuntime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{Exit: 3, Failures: 1, Output: "doctor: failures=1\n"}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})
	updateRollbackInstall = func(context.Context, string, string, string, commandRuntime, bool, io.Writer, io.Writer) error {
		return nil
	}
	updateRollbackDoctor = func(context.Context, string, commandRuntime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{}, nil
	}

	var stdout, stderr bytes.Buffer
	if code := runUpdate([]string{"--repo", repo}, &stdout, &stderr, runtime); code == 0 {
		t.Fatalf("runUpdate() code=0, want the candidate doctor failure to roll back; stdout=%q", stdout.String())
	}
	if got, err := os.ReadFile(legacyPath); err != nil || !bytes.Equal(got, originalContent) {
		t.Fatalf("config.json after rollback=%q err=%v, want the original bytes %q restored", got, err, originalContent)
	}
	if _, err := os.Stat(migratedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pfm.config.json after rollback: stat err=%v, want it gone (the migration's rename undone)", err)
	}
	if !strings.Contains(stderr.String(), "restored "+legacyPath+" to its pre-update state") {
		t.Fatalf("stderr=%q, want the restored-config-file report", stderr.String())
	}
}

// TestUpdateRollbackInstallSeesAnExistingConfigPath is a REGRESSION test for
// issue #24 finding 3 (M3 change D, the door-table guarantee): by the time
// the previous release's installer runs `install --yes --config
// <runtime.Config.Path>`, that file must already exist again — restoreUpdateHookFiles
// (D) runs before updateRollbackInstall specifically so the old binary never
// converges on defaults. Unfixed, the config file the migration renamed is
// never in the snapshot set, so it is still absent here.
func TestUpdateRollbackInstallSeesAnExistingConfigPath(t *testing.T) {
	runtime, repo, legacyPath, migratedPath, _ := updateConfigMigrationTestRuntime(t)

	oldBuild, oldInstall, oldRunDoctor := updateBuildCandidate, updateApplyInstall, updateRunDoctor
	oldRollbackInstall, oldRollbackDoctor := updateRollbackInstall, updateRollbackDoctor
	t.Cleanup(func() {
		updateBuildCandidate, updateApplyInstall, updateRunDoctor = oldBuild, oldInstall, oldRunDoctor
		updateRollbackInstall, updateRollbackDoctor = oldRollbackInstall, oldRollbackDoctor
	})
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		return os.WriteFile(output, []byte("new\n"), 0o755)
	}
	updateApplyInstall = func(context.Context, string, string, string, commandRuntime, bool, io.Writer, io.Writer) error {
		return os.Rename(legacyPath, migratedPath)
	}
	updateRunDoctor = func(context.Context, string, commandRuntime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{Exit: 3, Failures: 1, Output: "doctor: failures=1\n"}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})
	rollbackInstallSawConfig := false
	updateRollbackInstall = func(_ context.Context, _, _, _ string, rollbackRuntime commandRuntime, _ bool, _, _ io.Writer) error {
		if _, err := os.Stat(rollbackRuntime.Config.Path); err == nil {
			rollbackInstallSawConfig = true
		}
		return nil
	}
	updateRollbackDoctor = func(context.Context, string, commandRuntime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{}, nil
	}

	var stdout, stderr bytes.Buffer
	if code := runUpdate([]string{"--repo", repo}, &stdout, &stderr, runtime); code == 0 {
		t.Fatalf("runUpdate() code=0, want the candidate doctor failure to roll back; stdout=%q", stdout.String())
	}
	if !rollbackInstallSawConfig {
		t.Fatalf("rollback install ran with runtime.Config.Path=%q missing from disk", runtime.Config.Path)
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
