package hookentry

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/paths"
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
			if code := ClaudeLaunch(arguments, &bytes.Buffer{}, &stderr, runtime); code != 0 {
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
	if code := ClaudeLaunch([]string{"--version"}, &bytes.Buffer{}, &stderr, runtime); code != 127 {
		t.Fatalf("ClaudeLaunch() code=%d stderr=%q, want 127", code, stderr.String())
	}
	const want = "pfm claude launcher: no real Claude binary found\n"
	if stderr.String() != want {
		t.Fatalf("ClaudeLaunch() stderr=%q, want %q", stderr.String(), want)
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
