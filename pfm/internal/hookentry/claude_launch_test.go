package hookentry

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestClaudeLaunchResolvesAndDelegatesToLaunch(t *testing.T) {
	previousExec := LaunchExec
	t.Cleanup(func() { LaunchExec = previousExec })

	var execPath string
	var execArgs []string
	LaunchExec = func(path string, args, _ []string) error {
		execPath = path
		execArgs = append([]string(nil), args...)
		return nil
	}

	for _, test := range []struct {
		name  string
		setup func(t *testing.T, home, pathDir string) (configured, want string)
	}{
		{
			name: "configured binary",
			setup: func(t *testing.T, home, _ string) (string, string) {
				configured := filepath.Join(home, "configured", "claude")
				writeClaudeLaunchExecutable(t, configured)
				return configured, configured
			},
		},
		{
			name: "native version",
			setup: func(t *testing.T, home, _ string) (string, string) {
				version := filepath.Join(home, ".local", "share", "claude", "versions", "2.1.270")
				writeClaudeLaunchExecutable(t, version)
				return "", version
			},
		},
		{
			name: "PATH binary",
			setup: func(t *testing.T, _, pathDir string) (string, string) {
				binary := filepath.Join(pathDir, "claude")
				writeClaudeLaunchExecutable(t, binary)
				return "", binary
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			pathDir := filepath.Join(home, "path-bin")
			configured, wantBinary := test.setup(t, home, pathDir)
			t.Setenv("HOME", home)
			t.Setenv("PATH", pathDir)
			t.Setenv("PFM_LAUNCH_PASSTHROUGH", "1")
			t.Setenv("TMUX", "")
			execPath = ""
			execArgs = nil

			runtime := config.Runtime{
				Config: config.Config{Claude: config.Claude{Binary: configured}},
				Paths:  paths.Values{Home: home},
			}
			var stderr bytes.Buffer
			arguments := []string{"--resume", "session-id"}
			if code := ClaudeLaunch(arguments, &bytes.Buffer{}, &stderr, runtime, paths.OSEnv{}); code != 0 {
				t.Fatalf("ClaudeLaunch() code=%d stderr=%q, want 0", code, stderr.String())
			}
			if execPath != wantBinary {
				t.Fatalf("LaunchExec path=%q, want %q", execPath, wantBinary)
			}
			wantArgs := []string{wantBinary, "--resume", "session-id"}
			if !reflect.DeepEqual(execArgs, wantArgs) {
				t.Fatalf("LaunchExec args=%q, want %q", execArgs, wantArgs)
			}
		})
	}
}

func TestClaudeLaunchReturns127WhenClaudeBinaryIsAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", filepath.Join(home, "empty-path"))

	runtime := config.Runtime{Paths: paths.Values{Home: home}}
	var stderr bytes.Buffer
	if code := ClaudeLaunch([]string{"--version"}, &bytes.Buffer{}, &stderr, runtime, paths.OSEnv{}); code != 127 {
		t.Fatalf("ClaudeLaunch() code=%d stderr=%q, want 127", code, stderr.String())
	}
	const want = "pfm claude launcher: no real Claude binary found\n"
	if stderr.String() != want {
		t.Fatalf("ClaudeLaunch() stderr=%q, want %q", stderr.String(), want)
	}
}

// launchPIDEnvName pins the wire name of the re-entry marker: a rename of the
// production constant must be a deliberate edit here too.
const launchPIDEnvName = "PFM_CLAUDE_LAUNCH_PID"

// launchReentryFixture stubs LaunchExec, puts one fake claude on PATH, forces
// the passthrough branch, and sets the re-entry marker to marker (unset when
// empty). It returns the runtime, the resolved binary and a pointer to the
// recorded exec call.
func launchReentryFixture(t *testing.T, marker string) (config.Runtime, string, *launchExecCall) {
	t.Helper()
	previousExec := LaunchExec
	t.Cleanup(func() { LaunchExec = previousExec })
	call := &launchExecCall{}
	LaunchExec = func(path string, _, env []string) error {
		call.count++
		call.path = path
		call.env = append([]string(nil), env...)
		return nil
	}
	home := t.TempDir()
	pathDir := filepath.Join(home, "path-bin")
	binary := filepath.Join(pathDir, "claude")
	writeClaudeLaunchExecutable(t, binary)
	t.Setenv("HOME", home)
	t.Setenv("PATH", pathDir)
	t.Setenv("PFM_LAUNCH_PASSTHROUGH", "1")
	t.Setenv("TMUX", "")
	t.Setenv(launchPIDEnvName, marker)
	if marker == "" {
		if err := os.Unsetenv(launchPIDEnvName); err != nil {
			t.Fatal(err)
		}
	}
	return config.Runtime{Paths: paths.Values{Home: home}}, binary, call
}

type launchExecCall struct {
	count int
	path  string
	env   []string
}

func launchMarkerEntries(env []string) []string {
	var entries []string
	for _, entry := range env {
		if strings.HasPrefix(entry, launchPIDEnvName+"=") {
			entries = append(entries, entry)
		}
	}
	return entries
}

func TestClaudeLaunchRefusesToReenterItsOwnProcess(t *testing.T) {
	runtime, binary, call := launchReentryFixture(t, strconv.Itoa(os.Getpid()))
	var stderr bytes.Buffer
	code := ClaudeLaunch([]string{"--resume", "session-id"}, &bytes.Buffer{}, &stderr, runtime, paths.OSEnv{})
	if code != 127 {
		t.Fatalf("ClaudeLaunch() code=%d stderr=%q, want 127", code, stderr.String())
	}
	want := "pfm claude launcher re-entered itself via " + binary + "; refusing to loop\n"
	if stderr.String() != want {
		t.Fatalf("stderr=%q, want %q", stderr.String(), want)
	}
	if call.count != 0 {
		t.Fatalf("LaunchExec called %d times, want 0", call.count)
	}
}

func TestClaudeLaunchStampsItsPIDOnTheExecEnvironment(t *testing.T) {
	runtime, _, call := launchReentryFixture(t, "")
	var stderr bytes.Buffer
	arguments := []string{"--resume", "session-id"}
	if code := ClaudeLaunch(arguments, &bytes.Buffer{}, &stderr, runtime, paths.OSEnv{}); code != 0 {
		t.Fatalf("ClaudeLaunch() code=%d stderr=%q, want 0", code, stderr.String())
	}
	want := []string{launchPIDEnvName + "=" + strconv.Itoa(os.Getpid())}
	if got := launchMarkerEntries(call.env); !reflect.DeepEqual(got, want) {
		t.Fatalf("exec environment markers=%q, want %q", got, want)
	}
}

func TestClaudeLaunchPassesAnotherPIDsMarkerAndReplacesIt(t *testing.T) {
	runtime, binary, call := launchReentryFixture(t, "1")
	var stderr bytes.Buffer
	arguments := []string{"--resume", "session-id"}
	if code := ClaudeLaunch(arguments, &bytes.Buffer{}, &stderr, runtime, paths.OSEnv{}); code != 0 {
		t.Fatalf("ClaudeLaunch() code=%d stderr=%q, want 0", code, stderr.String())
	}
	if call.count != 1 || call.path != binary {
		t.Fatalf("LaunchExec calls=%d path=%q, want 1 call to %q", call.count, call.path, binary)
	}
	want := []string{launchPIDEnvName + "=" + strconv.Itoa(os.Getpid())}
	if got := launchMarkerEntries(call.env); !reflect.DeepEqual(got, want) {
		t.Fatalf("exec environment markers=%q, want %q", got, want)
	}
}

func TestClaudeLaunchStampsItsPIDOnPlainPassthrough(t *testing.T) {
	runtime, _, call := launchReentryFixture(t, "")
	var stderr bytes.Buffer
	arguments := []string{"--version"}
	if code := ClaudeLaunch(arguments, &bytes.Buffer{}, &stderr, runtime, paths.OSEnv{}); code != 0 {
		t.Fatalf("ClaudeLaunch() code=%d stderr=%q, want 0", code, stderr.String())
	}
	want := []string{launchPIDEnvName + "=" + strconv.Itoa(os.Getpid())}
	if got := launchMarkerEntries(call.env); !reflect.DeepEqual(got, want) {
		t.Fatalf("exec environment markers=%q, want %q", got, want)
	}
}

func writeClaudeLaunchExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}
