package testjail

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// keepAmbientChild marks the re-exec'd child that stands in for cmd/pfm's attach
// helper: it runs Run with KeepAmbientIdentity set.
const keepAmbientChild = "PFM_TEST_KEEP_AMBIENT_CHILD"

func TestMain(m *testing.M) {
	KeepAmbientIdentity = os.Getenv(keepAmbientChild) == "1"
	UnsetXDGConfigHome = os.Getenv(unsetXDGChild) == "1"
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

// The operator's ~/.local/bin holds pfm's own `claude` launcher shim; a jailed
// test that resolves a binary through PATH must never reach it. Run strips it
// before the first test, so the live process PATH carries no such entry.
func TestRunScrubsTheOperatorLocalBinFromPATH(t *testing.T) {
	home, err := paths.OSEnv{}.Home()
	if err != nil {
		t.Fatalf("user home: %v", err)
	}
	operatorBin := filepath.Join(home, ".local", "bin")
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry != "" && filepath.Clean(entry) == operatorBin {
			t.Fatalf("PATH %q still holds the operator's %s", os.Getenv("PATH"), operatorBin)
		}
	}
}

func TestScrubOperatorPATH(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	otherInstall := filepath.Join(root, "other", ".local", "share", "pfm", "install", "bin")
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(otherInstall, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	join := func(entries ...string) string { return strings.Join(entries, string(os.PathListSeparator)) }
	homeBin := filepath.Join(home, ".local", "bin")
	for _, test := range []struct {
		name, pathEnv, home, want string
	}{
		{"home bin removed", join("/usr/bin", homeBin, "/bin"), home, join("/usr/bin", "/bin")},
		{"unclean spelling removed", join(home+"/.local/./bin/", "/usr/bin"), home, "/usr/bin"},
		{"symlinked spelling removed", join(filepath.Join(link, ".local", "bin"), "/usr/bin"), home, "/usr/bin"},
		{"install bin under another home removed", join("/usr/bin", otherInstall), home, "/usr/bin"},
		{"empty entries and order kept", join("/b", "", homeBin, "/a", ""), home, join("/b", "", "/a", "")},
		{"no home still strips install bin", join(homeBin, otherInstall, "/usr/bin"), "", join(homeBin, "/usr/bin")},
		{"empty PATH stays empty", "", home, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := scrubOperatorPATH(test.pathEnv, test.home); got != test.want {
				t.Fatalf("scrubOperatorPATH(%q, %q)=%q, want %q", test.pathEnv, test.home, got, test.want)
			}
		})
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
	check := func(home, configPath string, extra map[string]any) {
		t.Helper()
		if configPath != filepath.Join(home, "pfm.config.json") {
			t.Fatalf("PFM_CONFIG=%q, want a config under %q", configPath, home)
		}
		body, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("seeded config = %q error %v", body, err)
		}
		var got map[string]any
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("parse seeded config = %q: %v", body, err)
		}
		want := map[string]any{"version": float64(2)}
		for key, value := range extra {
			want[key] = value
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seeded config = %q, want %v", body, want)
		}
	}
	check(os.Getenv(paths.EnvHome), os.Getenv(paths.EnvConfig), nil)
	for _, builder := range []struct {
		build func(*testing.T) string
		extra map[string]any
	}{{Fleet, nil}, {InstalledHome, map[string]any{}}} {
		root := builder.build(t)
		home := filepath.Join(root, "home")
		if builder.extra != nil {
			builder.extra["accounts"] = []any{map[string]any{
				"id": float64(1), "configDir": config.DefaultAccountDir(home, 1),
			}}
		}
		check(home, os.Getenv(paths.EnvConfig), builder.extra)
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
	check(envHome, envConfig, nil)
	runtime := CleanHome(
		t,
		[]string{"projects", "file-history", "tasks", "session-env"},
		map[string]string{"settings.json": "{}\n"},
	)
	check(runtime.Paths.Home, os.Getenv(paths.EnvConfig), map[string]any{"ask": map[string]any{"engine": "claude"}})
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
	runtime := CleanHome(
		t,
		[]string{"projects", "file-history", "tasks", "session-env"},
		map[string]string{"settings.json": "{}\n"},
	)
	want := filepath.Join(runtime.Paths.Home, ".config")
	if got := os.Getenv("XDG_CONFIG_HOME"); got != want {
		t.Fatalf("XDG_CONFIG_HOME=%q, want %q under the CleanHome jail", got, want)
	}
}

func TestCleanHomeStagesOneSessionStoreAndManagedCleanup(t *testing.T) {
	runtime := CleanHome(
		t,
		[]string{"projects", "file-history", "tasks", "session-env"},
		map[string]string{"settings.json": "{}\n"},
	)
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

	for _, id := range []int{1, 2} {
		link := filepath.Join(config.DefaultAccountDir(home, id), "settings.json")
		if target, err := os.Readlink(link); err != nil || target != filepath.Join(home, ".claude", "settings.json") {
			t.Fatalf("settings link=%q error=%v", target, err)
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

func TestStageAccountLinks(t *testing.T) {
	home := t.TempDir()
	accountDir := filepath.Join(t.TempDir(), "account")
	entries := []string{"existing", "created"}
	existing := filepath.Join(home, ".claude", entries[0])
	if err := os.MkdirAll(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "history.jsonl"), []byte("keep history"), 0o600); err != nil {
		t.Fatal(err)
	}
	StageAccountLinks(
		t,
		home,
		accountDir,
		entries,
		map[string]string{"settings.json": "{}\n", "history.jsonl": "replace history"},
	)
	if raw, err := os.ReadFile(
		filepath.Join(accountDir, "history.jsonl"),
	); err != nil ||
		string(raw) != "keep history" {
		t.Fatalf("existing file=%q error=%v", raw, err)
	}
	raw, err := os.ReadFile(filepath.Join(accountDir, "settings.json"))
	if err != nil || string(raw) != "{}\n" {
		t.Fatalf("file seed=%q error=%v", raw, err)
	}
	info, err := os.Stat(filepath.Join(home, ".claude", "settings.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode=%v error=%v", info, err)
	}
	if target, err := os.Readlink(
		filepath.Join(accountDir, "settings.json"),
	); err != nil ||
		target != filepath.Join(home, ".claude", "settings.json") {
		t.Fatalf("file link=%q error=%v", target, err)
	}
	for _, entry := range entries {
		store := filepath.Join(home, ".claude", entry)
		if info, err := os.Stat(store); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("store %s = %v, %v; want a 0700 directory", store, info, err)
		}
		link := filepath.Join(accountDir, entry)
		if target, err := os.Readlink(link); err != nil || target != store {
			t.Fatalf("%s → %q, %v; want %s", link, target, err, store)
		}
		if resolved, err := filepath.EvalSymlinks(link); err != nil || resolved != store {
			t.Fatalf("resolve %s = %q, %v; want %s", link, resolved, err, store)
		}
	}
}

func TestStageAccountLinksErrors(t *testing.T) {
	const marker = "PFM_TEST_ACCOUNT_LINK_ERROR"
	if failure := os.Getenv(marker); failure != "" {
		home, accountDir := t.TempDir(), t.TempDir()
		blocked := filepath.Join(accountDir, "entry")
		if failure == "store" {
			blocked = filepath.Join(home, ".claude")
		}
		if err := os.WriteFile(blocked, []byte("blocked"), 0o600); err != nil {
			t.Fatal(err)
		}
		StageAccountLinks(t, home, accountDir, []string{"entry"}, nil)
		return
	}
	for _, failure := range []string{"store", "link"} {
		t.Run(failure, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestStageAccountLinksErrors$")
			command.Env = envWithout(marker, marker+"="+failure)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatal("StageAccountLinks succeeded with a blocked path")
			}
			want := "entry"
			if failure == "store" {
				want = filepath.Join(".claude", "entry")
			}
			if !strings.Contains(string(output), want) {
				t.Fatalf("error output %q does not name %s", output, want)
			}
		})
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

func TestRunCreatesDefaultAccountDir(t *testing.T) {
	dir := config.DefaultAccountDir(os.Getenv(paths.EnvHome), 1)
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("account dir %s: mode %v, want real directory (0700)", dir, info.Mode())
	}
}

func TestJailHomeSetupFailure(t *testing.T) {
	base := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(base, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cleanup, err := jailHome(base)
	defer cleanup()
	if !errors.Is(err, syscall.ENOTDIR) || !strings.HasPrefix(err.Error(), "create jailed home under "+base+": ") {
		t.Fatalf("jail setup error = %v, want contextual not-a-directory error", err)
	}
}

func TestInstalledHomeUsesAnAccountDirectory(t *testing.T) {
	root := InstalledHome(t)
	runtime, err := config.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	want := config.DefaultAccountDir(filepath.Join(root, "home"), 1)
	if len(runtime.Config.Accounts) != 1 || runtime.Config.Accounts[0].ConfigDir != want {
		t.Fatalf("accounts=%+v want=%s", runtime.Config.Accounts, want)
	}
	info, err := os.Lstat(want)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("account=%v error=%v", info, err)
	}
	if got := os.Getenv(paths.EnvClaudeRoots); got != filepath.Join(root, "claude") {
		t.Fatalf("roots=%s", got)
	}
}

func TestStageGlobalAgentsUsesTheStore(t *testing.T) {
	home := t.TempDir()
	StageGlobalAgents(t, home)
	entries, err := os.ReadDir(filepath.Join(home, ".claude", "agents"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("store agents=%v error=%v", entries, err)
	}
}

func TestStageClaudePlugins(t *testing.T) {
	store := filepath.Join(t.TempDir(), ".claude")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "settings.json"), []byte(`{"keep":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	StageClaudePlugins(t, store, []string{"fixture@market"})
	raw, err := os.ReadFile(filepath.Join(store, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Keep           bool
		EnabledPlugins map[string]bool
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	if !settings.Keep || !settings.EnabledPlugins["fixture@market"] {
		t.Fatalf("settings=%s", raw)
	}
	raw, err = os.ReadFile(filepath.Join(store, "plugins", "installed_plugins.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Plugins map[string][]struct{ InstallPath string }
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	entries := record.Plugins["fixture@market"]
	if len(entries) != 1 {
		t.Fatalf("record=%s", raw)
	}
	if info, err := os.Stat(entries[0].InstallPath); err != nil || !info.IsDir() {
		t.Fatalf("install=%v error=%v", info, err)
	}
}

// TestRunClearsTheLoginDefault: a host whose login shell exports the login
// default (CLAUDE_CONFIG_DIR plus its sentinel) must not reach a jailed test.
func TestRunClearsTheLoginDefault(t *testing.T) {
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "PFM_CLAUDE_CONFIG_DIR_DEFAULT"} {
		if value := os.Getenv(name); value != "" {
			t.Errorf("%s=%q survived testjail.Run", name, value)
		}
	}
}
