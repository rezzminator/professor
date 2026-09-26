package testjail

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestMain(m *testing.M) { os.Exit(Run(m)) }

// A `go` child (the self-update rebuild in internal/update, a `go run`) derives
// GOCACHE, GOPATH and GOMODCACHE from HOME when they are unset. Every jail rehomes HOME, so the
// cache would land under the jail root and a child still compiling at teardown
// makes RemoveAll fail with "directory not empty" — the flake
// TestKillSelfResolveAndInternalCLI showed under load (5/6 red). Run pins all
// three before any jail exists: each must be set and sit outside every jail root.
func TestRunPinsGoCacheOutsideTheJail(t *testing.T) {
	root := Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatalf("resolve the jail's paths: %v", err)
	}
	home := values.Home
	for _, name := range []string{"GOCACHE", "GOPATH", "GOMODCACHE"} {
		value := os.Getenv(name)
		if value == "" {
			t.Fatalf("%s is unset after Run — a go child would write under the jailed home %s", name, home)
		}
		if !filepath.IsAbs(value) {
			t.Fatalf("%s %q is not absolute", name, value)
		}
		if strings.HasPrefix(value, root+string(filepath.Separator)) ||
			strings.HasPrefix(value, home+string(filepath.Separator)) {
			t.Fatalf("%s %q sits under the jail (root %s, home %s)", name, value, root, home)
		}
	}
}

func TestPinGoDirsPinsTelemetryOutsideTheJail(t *testing.T) {
	t.Setenv("TEST_TELEMETRY_DIR", "")
	t.Setenv("GOMODCACHE", "")
	if code := pinGoDirs(); code != 0 {
		t.Fatalf("pin Go directories code=%d", code)
	}

	root := Fleet(t)
	telemetryDirectory := os.Getenv("TEST_TELEMETRY_DIR")
	if telemetryDirectory == "" {
		t.Fatal("TEST_TELEMETRY_DIR is unset after pinGoDirs")
	}
	if !filepath.IsAbs(telemetryDirectory) {
		t.Fatalf("TEST_TELEMETRY_DIR %q is not absolute", telemetryDirectory)
	}
	wantTelemetry := filepath.Join(os.Getenv("GOCACHE"), "telemetry")
	if telemetryDirectory != wantTelemetry {
		t.Fatalf("TEST_TELEMETRY_DIR=%q, want %q under GOCACHE", telemetryDirectory, wantTelemetry)
	}
	if os.Getenv("GOMODCACHE") == "" {
		t.Fatal("GOMODCACHE is unset after pinGoDirs")
	}
	if strings.HasPrefix(telemetryDirectory, root+string(filepath.Separator)) {
		t.Fatalf("TEST_TELEMETRY_DIR %q sits under the fleet root %q", telemetryDirectory, root)
	}
	assertGoTelemetryDirectory(t, telemetryDirectory, root)
}

func TestPinGoDirsPreservesConfiguredGoCacheDirectories(t *testing.T) {
	configured := map[string]string{
		"GOCACHE":    filepath.Join(string(filepath.Separator), "configured", "build-cache"),
		"GOPATH":     filepath.Join(string(filepath.Separator), "configured", "go-path"),
		"GOMODCACHE": filepath.Join(string(filepath.Separator), "configured", "module-cache"),
	}
	for name, value := range configured {
		t.Setenv(name, value)
	}
	t.Setenv("TEST_TELEMETRY_DIR", filepath.Join(string(filepath.Separator), "configured", "telemetry"))
	if code := pinGoDirs(); code != 0 {
		t.Fatalf("pin Go directories code=%d", code)
	}
	for name, want := range configured {
		if got := os.Getenv(name); got != want {
			t.Errorf("%s=%q, want configured directory %q", name, got, want)
		}
	}
}

func TestPinGoDirsPreservesConfiguredTelemetryDirectory(t *testing.T) {
	configured := filepath.Join(os.Getenv("GOCACHE"), "configured-telemetry")
	t.Setenv("TEST_TELEMETRY_DIR", configured)
	if code := pinGoDirs(); code != 0 {
		t.Fatalf("pin Go directories code=%d", code)
	}

	root := Fleet(t)
	if got := os.Getenv("TEST_TELEMETRY_DIR"); got != configured {
		t.Fatalf("TEST_TELEMETRY_DIR=%q, want configured directory %q", got, configured)
	}
	assertGoTelemetryDirectory(t, configured, root)
}

func assertGoTelemetryDirectory(t *testing.T, want, root string) {
	t.Helper()
	command := exec.Command("go", "env", "GOTELEMETRYDIR")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("query Go telemetry directory: %v: %s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != want {
		t.Fatalf("go env GOTELEMETRYDIR=%q, want inherited TEST_TELEMETRY_DIR %q", got, want)
	}
	jailedTelemetry := filepath.Join(root, "home", ".config", "go", "telemetry")
	if _, err := os.Stat(jailedTelemetry); !os.IsNotExist(err) {
		t.Fatalf("jailed Go telemetry directory %q exists or could not be checked: %v", jailedTelemetry, err)
	}
}

// Run keeps child tools' XDG files inside the package jail.
func TestRunPinsXDGConfigHomeInsideTheJail(t *testing.T) {
	home := os.Getenv(paths.EnvHome)
	if home == "" {
		t.Fatal("PFM_HOME is unset — Run's own jail did not pin it")
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		t.Fatal("XDG_CONFIG_HOME is unset after Run")
	}
	if !filepath.IsAbs(xdg) {
		t.Fatalf("XDG_CONFIG_HOME %q is not absolute", xdg)
	}
	if !strings.HasPrefix(xdg, home+string(filepath.Separator)) {
		t.Fatalf("XDG_CONFIG_HOME %q does not sit under the jailed PFM_HOME %q", xdg, home)
	}
}

func TestEveryJailPinsAndSeedsPFMConfig(t *testing.T) {
	check := func(home, configPath string) {
		t.Helper()
		if configPath != filepath.Join(home, "pfm.config.json") {
			t.Fatalf("PFM_CONFIG=%q, want a config under %q", configPath, home)
		}
		body, err := os.ReadFile(configPath)
		if err != nil || strings.TrimSpace(string(body)) != `{"version":2}` {
			t.Fatalf("seeded config = %q error %v", body, err)
		}
	}
	check(os.Getenv(paths.EnvHome), os.Getenv(paths.EnvConfig))
	for _, builder := range []func(*testing.T) string{Fleet, InstalledHome} {
		root := builder(t)
		check(filepath.Join(root, "home"), os.Getenv(paths.EnvConfig))
	}
	_, environment := FleetEnv(t)
	var envHome, envConfig string
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, paths.EnvHome+"="); ok {
			envHome = value
		}
		if value, ok := strings.CutPrefix(entry, paths.EnvConfig+"="); ok {
			envConfig = value
		}
	}
	check(envHome, envConfig)
	runtime := CleanHome(t)
	check(runtime.Paths.Home, os.Getenv(paths.EnvConfig))
}

// TestRunScrubsAmbientIdentity pins the jail's own scrub: an executor's shell
// carries a live tmux pane, chat socket, or host Claude/Codex session id, and
// none of it may reach a jailed test — a test that needs one sets it back
// with t.Setenv.
func TestRunScrubsAmbientIdentity(t *testing.T) {
	for _, name := range []string{
		"TMUX",
		"TMUX_PANE",
		"CHAT_INJECT_SOCKET",
		"CLAUDE_CODE_SESSION_ID",
		"CODEX_THREAD_ID",
	} {
		if value := os.Getenv(name); value != "" {
			t.Fatalf("%s=%q after Run — ambient identity leaked into the jail", name, value)
		}
	}
}

// TestFleetPinsXDGConfigHomeInsideTheJail pins the per-test-fleet door.
func TestFleetPinsXDGConfigHomeInsideTheJail(t *testing.T) {
	root := Fleet(t)
	want := filepath.Join(root, "home", ".config")
	if got := os.Getenv("XDG_CONFIG_HOME"); got != want {
		t.Fatalf("XDG_CONFIG_HOME=%q, want %q under the fleet root", got, want)
	}
}

// TestCleanHomePinsXDGConfigHomeInsideTheJail pins the CleanHome door.
func TestCleanHomePinsXDGConfigHomeInsideTheJail(t *testing.T) {
	runtime := CleanHome(t)
	want := filepath.Join(runtime.Paths.Home, ".config")
	if got := os.Getenv("XDG_CONFIG_HOME"); got != want {
		t.Fatalf("XDG_CONFIG_HOME=%q, want %q under the CleanHome jail", got, want)
	}
}

func TestCleanHomeStagesOneSessionStoreAndManagedCleanup(t *testing.T) {
	runtime := CleanHome(t)
	home := runtime.Paths.Home
	if got := os.Getenv(paths.EnvClaudeRoots); got != filepath.Join(home, ".claude", "projects") {
		t.Fatalf("Claude roots=%q", got)
	}
	for _, entry := range []string{"projects", "file-history", "tasks", "session-env"} {
		store := filepath.Join(home, ".claude", entry)
		if info, err := os.Stat(store); err != nil || !info.IsDir() {
			t.Fatalf("store %s = %v, %v", store, info, err)
		}
		for _, account := range []string{"1", "2"} {
			link := filepath.Join(home, ".cc", account, entry)
			if target, err := os.Readlink(link); err != nil || target != store {
				t.Fatalf("%s → %q, %v; want %s", link, target, err, store)
			}
		}
	}
	managed := filepath.Join(runtime.Paths.ManagedSettingsDir, "pfm.json")
	if raw, err := os.ReadFile(managed); err != nil || strings.TrimSpace(string(raw)) != `{"cleanupPeriodDays":36500}` {
		t.Fatalf("managed settings=%q error=%v", raw, err)
	}
	if raw, err := os.ReadFile(
		filepath.Join(home, ".zshrc"),
	); err != nil ||
		!strings.Contains(string(raw), checkoutRoot()+"/pfm/internal/installer/assets/shim/pfm.zsh") {
		t.Fatalf("clone source line=%q error=%v", raw, err)
	}
}

func TestInstalledHomeStagesCloneSourceLine(t *testing.T) {
	root := InstalledHome(t)
	path := filepath.Join(root, "home", ".zshrc")
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), checkoutRoot()+"/pfm/internal/installer/assets/shim/pfm.zsh") {
		t.Fatalf("installed source line=%q error=%v", raw, err)
	}
}
