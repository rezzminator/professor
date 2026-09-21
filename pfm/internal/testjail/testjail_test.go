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

// TestRunPinsXDGConfigHomeInsideTheJail pins L3-F9's package-wide door: Run
// (via jailHome) sets PFM_HOME before any individual test runs, and must pin
// XDG_CONFIG_HOME alongside it — a package that never builds a jail of its
// own (never calls Fleet/CleanHome) must still be unable to resolve the
// operator's ambient XDG_CONFIG_HOME the same way it cannot resolve their
// real PFM_HOME.
func TestRunPinsXDGConfigHomeInsideTheJail(t *testing.T) {
	home := os.Getenv(paths.EnvHome)
	if home == "" {
		t.Fatal("PFM_HOME is unset — Run's own jail did not pin it")
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		t.Fatal("XDG_CONFIG_HOME is unset after Run — an ambient value from the operator's shell would reach " +
			"config.LoadRuntime unpinned")
	}
	if !filepath.IsAbs(xdg) {
		t.Fatalf("XDG_CONFIG_HOME %q is not absolute", xdg)
	}
	if !strings.HasPrefix(xdg, home+string(filepath.Separator)) {
		t.Fatalf("XDG_CONFIG_HOME %q does not sit under the jailed PFM_HOME %q", xdg, home)
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
