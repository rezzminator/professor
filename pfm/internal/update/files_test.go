package update

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// updateConfigMigrationTestRuntime is updateRollbackTestRuntime, plus a
// pre-split legacy config.json on disk and a runtime whose Config.Path names
// it — the shape a host has right before the candidate's own `install --yes`
// runs the v0.74.0 migration (config.json -> pfm.config.json) inside the
// candidate process only.
func updateConfigMigrationTestRuntime(
	t *testing.T,
) (runtime pfmconfig.Runtime, repo, legacyPath, migratedPath string, originalContent []byte) {
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
	updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return os.Rename(legacyPath, migratedPath)
	}
	updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{Exit: 3, Failures: 1, Output: "doctor: failures=1\n"}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})
	updateRollbackInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return nil
	}
	updateRollbackDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{}, nil
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--repo", repo}, &stdout, &stderr, runtime); code == 0 {
		t.Fatalf("Run() code=0, want the candidate doctor failure to roll back; stdout=%q", stdout.String())
	}
	if got, err := os.ReadFile(legacyPath); err != nil || !bytes.Equal(got, originalContent) {
		t.Fatalf("config.json after rollback=%q err=%v, want the original bytes %q restored", got, err, originalContent)
	}
	if _, err := os.Stat(migratedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pfm.config.json after rollback: stat err=%v, want it gone (the migration's rename undone)", err)
	}
	physicalLegacyPath, err := filepath.EvalSymlinks(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "restored "+physicalLegacyPath+" to its pre-update state") {
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
	updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return os.Rename(legacyPath, migratedPath)
	}
	updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{Exit: 3, Failures: 1, Output: "doctor: failures=1\n"}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})
	rollbackInstallSawConfig := false
	updateRollbackInstall = func(_ context.Context, _, _, _ string, rollbackRuntime pfmconfig.Runtime, _ bool, _, _ io.Writer) error {
		if _, err := os.Stat(rollbackRuntime.Config.Path); err == nil {
			rollbackInstallSawConfig = true
		}
		return nil
	}
	updateRollbackDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{}, nil
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--repo", repo}, &stdout, &stderr, runtime); code == 0 {
		t.Fatalf("Run() code=0, want the candidate doctor failure to roll back; stdout=%q", stdout.String())
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

// updateRollbackAfterInstall runs a real Run whose candidate install is
// install and whose gating doctor runs between and then fails, so the update
// rolls back; it returns stderr.
func updateRollbackAfterInstall(
	t *testing.T,
	runtime pfmconfig.Runtime,
	repo string,
	install func() error,
	between func(),
) string {
	t.Helper()
	oldBuild, oldInstall, oldRunDoctor := updateBuildCandidate, updateApplyInstall, updateRunDoctor
	oldRollbackInstall, oldRollbackDoctor := updateRollbackInstall, updateRollbackDoctor
	t.Cleanup(func() {
		updateBuildCandidate, updateApplyInstall, updateRunDoctor = oldBuild, oldInstall, oldRunDoctor
		updateRollbackInstall, updateRollbackDoctor = oldRollbackInstall, oldRollbackDoctor
	})
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		return os.WriteFile(output, []byte("new\n"), 0o755)
	}
	updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return install()
	}
	updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		between()
		return doctorOutcome{Exit: 3, Failures: 1, Output: "doctor: failures=1\n"}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})
	updateRollbackInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return nil
	}
	updateRollbackDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{}, nil
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--repo", repo}, &stdout, &stderr, runtime); code == 0 {
		t.Fatalf("Run() code=0, want the candidate doctor failure to roll back; stdout=%q", stdout.String())
	}
	return stderr.String()
}

func writeUpdateFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The MCP registration files install rewrites — a Codex config.toml, the
// OpenCode opencode.jsonc, a Claude registry, ~/.mcp.json and the MCP
// ownership ledger — are snapshotted: rollback restores each one untouched
// since install to its pre-update bytes (removing one absent before), and
// names one changed since install as an MCP registration left as is.
func TestUpdateRollbackRestoresTheMCPRegistrationsTheInstallRewrote(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	runtime, repo := updateRollbackTestRuntime(t)
	home := runtime.Paths.Home
	codexHome := filepath.Join(home, ".codex")
	runtime.Config = pfmconfig.Config{
		Accounts:      []pfmconfig.Account{{ID: 1, Implicit: true}},
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: codexHome}},
	}
	codexConfig := filepath.Join(codexHome, "config.toml")
	claudeRegistry := filepath.Join(home, ".claude.json")
	mcpJSON := filepath.Join(home, ".mcp.json")
	openCode := installer.OpenCodeConfigPath(home)
	ledger := filepath.Join(home, ".local", "share", "pfm", "install", "mcp-ownership.json")
	before := map[string]string{
		codexConfig:    "model = \"operator\"\n",
		claudeRegistry: "{\"mcpServers\":{}}\n",
		mcpJSON:        "{\"mcpServers\":{\"operator\":{}}}\n",
	}
	for path, content := range before {
		writeUpdateFixtureFile(t, path, content)
	}
	stderr := updateRollbackAfterInstall(t, runtime, repo, func() error {
		for _, path := range []string{codexConfig, claudeRegistry, mcpJSON, openCode, ledger} {
			writeUpdateFixtureFile(t, path, "written by the candidate install\n")
		}
		return nil
	}, func() {
		writeUpdateFixtureFile(t, claudeRegistry, "{\"operator\":\"saved while the update ran\"}\n")
	})
	for _, path := range []string{codexConfig, mcpJSON} {
		if got, err := os.ReadFile(path); err != nil || string(got) != before[path] {
			t.Fatalf("%s after rollback = %q, %v; want its pre-update bytes %q", path, got, err, before[path])
		}
	}
	for _, path := range []string{openCode, ledger} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s after rollback: stat err=%v, want it removed (absent before the update)", path, err)
		}
	}
	got, err := os.ReadFile(claudeRegistry)
	if err != nil || !strings.Contains(string(got), "saved while the update ran") {
		t.Fatalf("Claude registry after rollback = %q, %v; want the concurrent edit kept", got, err)
	}
	physical, err := filepath.EvalSymlinks(claudeRegistry)
	if err != nil {
		t.Fatal(err)
	}
	want := "MCP registration " + physical +
		" changed after the update's install wrote it; left as is — reconcile it by hand"
	if !strings.Contains(stderr, want) {
		t.Fatalf("stderr=%q, want %q", stderr, want)
	}
}

// A config JSON changed after the update's install is named as a config file,
// not as a hook file.
func TestUpdateRollbackResidueNamesAChangedConfigFileAsAConfigFile(t *testing.T) {
	runtime, repo, legacyPath, migratedPath, _ := updateConfigMigrationTestRuntime(t)
	stderr := updateRollbackAfterInstall(t, runtime, repo, func() error {
		return os.Rename(legacyPath, migratedPath)
	}, func() {
		writeUpdateFixtureFile(t, migratedPath, "{\"version\":2,\"theme\":\"edited while the update ran\"}")
	})
	physical, err := filepath.EvalSymlinks(migratedPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "config file " + physical +
		" changed after the update's install wrote it; left as is — reconcile it by hand"
	if !strings.Contains(stderr, want) {
		t.Fatalf("stderr=%q, want %q", stderr, want)
	}
}

// $HOME/.claude.json is rewritten by install even when no account wires it
// (every account has its own ConfigDir): pfm's legacy entries there go. The
// snapshot therefore covers it too, and rollback restores its pre-update bytes.
func TestUpdateRollbackRestoresTheHomeClaudeRegistryNoAccountWires(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	runtime, repo := updateRollbackTestRuntime(t)
	home := runtime.Paths.Home
	runtime.Config = pfmconfig.Config{
		Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: filepath.Join(home, ".cc", "1")}},
	}
	homeRegistry := filepath.Join(home, ".claude.json")
	before := "{\"mcpServers\":{\"chat\":{\"command\":\"pfm\"}}}\n"
	writeUpdateFixtureFile(t, homeRegistry, before)
	updateRollbackAfterInstall(t, runtime, repo, func() error {
		writeUpdateFixtureFile(t, homeRegistry, "{\"mcpServers\":{}}\n")
		return nil
	}, func() {})
	if got, err := os.ReadFile(homeRegistry); err != nil || string(got) != before {
		t.Fatalf("%s after rollback = %q, %v; want its pre-update bytes %q", homeRegistry, got, err, before)
	}
}
