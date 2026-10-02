package testjail

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// keepAmbientChild marks the re-exec'd child that stands in for cmd/pfm's attach
// helper: it runs Run with KeepAmbientIdentity set.
const keepAmbientChild = "PFM_TEST_KEEP_AMBIENT_CHILD"

func TestMain(m *testing.M) {
	KeepAmbientIdentity = os.Getenv(keepAmbientChild) == "1"
	os.Exit(Run(m))
}

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

func TestRunPinsSIDDirInsideTheJail(t *testing.T) {
	home := os.Getenv(paths.EnvHome)
	sid := os.Getenv(paths.EnvSIDDir)
	if sid != filepath.Join(home, "sid") {
		t.Fatalf("%s=%q, want sid directory under jail home %q", paths.EnvSIDDir, sid, home)
	}
	if info, err := os.Stat(sid); err != nil || !info.IsDir() {
		t.Fatalf("sid directory %q: %v, %v", sid, info, err)
	}
}

func TestRunPreservesCallerSIDDir(t *testing.T) {
	const marker = "PFM_TEST_CALLER_SID_DIR"
	if want := os.Getenv(marker); want != "" {
		if got := os.Getenv(paths.EnvSIDDir); got != want {
			t.Fatalf("%s=%q, want caller value %q", paths.EnvSIDDir, got, want)
		}
		return
	}
	want := filepath.Join(t.TempDir(), "caller-sid")
	command := exec.Command(os.Args[0], "-test.run=^TestRunPreservesCallerSIDDir$")
	command.Env = append(os.Environ(), paths.EnvSIDDir+"="+want, marker+"="+want)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child test with caller SID dir: %v: %s", err, output)
	}
}

// The gate's step profiler exports BASH_ENV (set -E, an ERR trap) into every
// step. Run drops it for the Go test process, so a shell the test starts writes
// no record and a tmux pane launched as `bash -c '<launch>'` still becomes the
// launched program. The child re-runs this test under Run: it must see BASH_ENV
// unset, and so must a process it starts; with the attach helper's
// KeepAmbientIdentity the ambient identity stays and BASH_ENV is gone anyway.
func TestRunClearsBashEnv(t *testing.T) {
	const marker = "PFM_TEST_BASH_ENV_CHILD"
	if os.Getenv(marker) != "" {
		if value, set := os.LookupEnv("BASH_ENV"); set {
			t.Fatalf("BASH_ENV=%q after Run, want it unset", value)
		}
		if os.Getenv(keepAmbientChild) == "1" {
			if got := os.Getenv("TMUX"); got != "ambient-tmux" {
				t.Fatalf("TMUX=%q with KeepAmbientIdentity, want the ambient %q", got, "ambient-tmux")
			}
		}
		listing, err := exec.Command("env").Output()
		if err != nil {
			t.Fatalf("list the environment of a child: %v", err)
		}
		for _, line := range strings.Split(string(listing), "\n") {
			if strings.HasPrefix(line, "BASH_ENV=") {
				t.Fatalf("a child process inherited %q, want BASH_ENV unset", line)
			}
		}
		return
	}
	tracer := filepath.Join(t.TempDir(), "tracer.sh")
	if err := os.WriteFile(tracer, []byte("set -E\ntrap : ERR\n"), 0o600); err != nil {
		t.Fatalf("write the tracer env file: %v", err)
	}
	for _, tc := range []struct {
		name  string
		extra []string
	}{
		{"exported", []string{"BASH_ENV=" + tracer}},
		{"not exported", nil},
		{"attach helper", []string{"BASH_ENV=" + tracer, keepAmbientChild + "=1", "TMUX=ambient-tmux"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestRunClearsBashEnv$")
			command.Env = envWithout("BASH_ENV", append(tc.extra, marker+"=1")...)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("child test (%s): %v: %s", tc.name, err, output)
			}
		})
	}
}

// envWithout is the parent's environment with name removed, then extra added, so
// a case that wants BASH_ENV absent gets it even when the gate exported one.
func envWithout(name string, extra ...string) []string {
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, name+"=") {
			env = append(env, entry)
		}
	}
	// extra may restore the removed name: it is appended after the filter.
	return append(env, extra...)
}

// The profiler's variables are the one channel a step's profile reaches a test
// process and its helpers by; clearing BASH_ENV must leave all four as exported.
func TestRunPassesProfilerVariablesThrough(t *testing.T) {
	const marker = "PFM_TEST_PROFILER_VARS_CHILD"
	want := map[string]string{
		paths.EnvTestDeadlineEpoch: "4102444800",
		paths.EnvTestProfile:       "cpu",
		paths.EnvTestProfileParent: "4242",
	}
	if os.Getenv(marker) != "" {
		for name, value := range want {
			if got := os.Getenv(name); got != value {
				t.Fatalf("%s=%q in the test process, want the exported %q", name, got, value)
			}
		}
		artifacts := os.Getenv(marker)
		if got := os.Getenv(paths.EnvTestArtifactDir); got != artifacts {
			t.Fatalf("%s=%q in the test process, want the exported %q", paths.EnvTestArtifactDir, got, artifacts)
		}
		return
	}
	artifacts := filepath.Join(t.TempDir(), "artifacts")
	command := exec.Command(os.Args[0], "-test.run=^TestRunPassesProfilerVariablesThrough$")
	command.Env = envWithout("BASH_ENV", marker+"="+artifacts, paths.EnvTestArtifactDir+"="+artifacts)
	for name, value := range want {
		command.Env = append(command.Env, name+"="+value)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child test with the profiler variables exported: %v: %s", err, output)
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
