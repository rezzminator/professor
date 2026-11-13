package testjail

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// A chat's exported store paths and install mark never reach a jailed test, but
// a jailed test that re-execs its own binary as a helper hands it the jail's
// store paths and mark (hookentry's TestSeatHostPTY launch modes): Run keeps
// those, and only those.
func TestRunKeepsTheStorePathsAJailedTestHandsItsHelper(t *testing.T) {
	const marker = "PFM_TEST_STORE_PATHS_CHILD"
	const markMarker = "PFM_TEST_INSTALL_MARK_CHILD"
	if want, child := os.LookupEnv(marker); child {
		if got := os.Getenv("PFM_STATE_DB"); got != want {
			t.Fatalf("PFM_STATE_DB=%q after Run, want %q", got, want)
		}
		if got, want := os.Getenv(paths.EnvInstallMark), os.Getenv(markMarker); got != want {
			t.Fatalf("%s=%q after Run, want %q", paths.EnvInstallMark, got, want)
		}
		return
	}
	for _, tc := range []struct {
		name string
		env  []string
	}{
		{"helper of a jailed test", append(os.Environ(),
			"PFM_STATE_DB=/jail/pfm.db", marker+"=/jail/pfm.db",
			paths.EnvInstallMark+"=0123456789abcdef", markMarker+"=0123456789abcdef")},
		{"started by a chat", envWithout(jailMarkerEnv,
			"PFM_STATE_DB=/jail/pfm.db", marker+"=",
			paths.EnvInstallMark+"=fedcba9876543210", markMarker+"=")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestRunKeepsTheStorePathsAJailedTestHandsItsHelper$")
			command.Env = tc.env
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("child test (%s): %v: %s", tc.name, err, output)
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

// Every jail marks the processes it starts with its own home's install mark,
// over the operator's inherited one, so no stop acting for the operator's
// install owns a jailed child.
func TestJailMarksItsChildrenWithTheJailHomesMark(t *testing.T) {
	for _, row := range []struct {
		name  string
		build func(*testing.T) (home string, environment []string)
	}{
		{"Fleet", func(t *testing.T) (string, []string) {
			return filepath.Join(Fleet(t), "home"), os.Environ()
		}},
		{"FleetEnv", func(t *testing.T) (string, []string) {
			root, environment := FleetEnv(t)
			return filepath.Join(root, "home"), environment
		}},
		{"CleanHome", func(t *testing.T) (string, []string) {
			runtime := CleanHome(t, []string{"projects"}, map[string]string{"settings.json": "{}\n"})
			return runtime.Paths.Home, os.Environ()
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Setenv(paths.EnvInstallMark, "operator-mark")
			// Resolved before the jail: CleanHome's PATH holds only its staged shims.
			envBinary, err := exec.LookPath("env")
			if err != nil {
				t.Fatalf("resolve env: %v", err)
			}
			home, environment := row.build(t)
			command := exec.Command(envBinary)
			command.Env = environment
			listing, err := command.Output()
			if err != nil {
				t.Fatalf("list a jailed child's environment: %v", err)
			}
			var marks []string
			for _, line := range strings.Split(string(listing), "\n") {
				if mark, ok := strings.CutPrefix(line, paths.EnvInstallMark+"="); ok {
					marks = append(marks, mark)
				}
			}
			if want := []string{paths.InstallMark(home)}; !slices.Equal(marks, want) {
				t.Fatalf("jailed child %s = %q; want %q, jail home %s's", paths.EnvInstallMark, marks, want, home)
			}
		})
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
