package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/mockengine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

// TestBuiltBinaryAnswersByTheNameItIsInstalledUnder builds the real binary and
// runs it through symlinks named after each engine — the door every jail uses.
func TestBuiltBinaryAnswersByTheNameItIsInstalledUnder(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("TOOLCHAIN-MISSING go: cannot build cmd/mock-engine")
	}
	root := t.TempDir()
	binary, err := testjail.MockEngineBinary("../..", root)
	if err != nil {
		t.Fatalf("go build: %v", err)
	}
	scenario := filepath.Join(root, "scenario.json")
	if err := (mockengine.Scenario{Version: "9.9.9 (Fixture)"}).Write(scenario); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "codex", "opencode"} {
		link := filepath.Join(root, name)
		if err := os.Symlink(binary, link); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(link, "--version")
		command.Env = append(os.Environ(), mockengine.EnvScenario+"="+scenario)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		if err := command.Run(); err != nil || strings.TrimSpace(stdout.String()) != "9.9.9 (Fixture)" {
			t.Fatalf("%s --version: err=%v stdout=%q stderr=%q", name, err, stdout.String(), stderr.String())
		}
	}
	// An argv[0] that names no engine is refused with the usage code, never
	// silently one engine.
	stray := filepath.Join(root, "2.1.238")
	if err := os.Symlink(binary, stray); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(stray, "--version")
	command.Env = append(os.Environ(), mockengine.EnvScenario+"="+scenario, mockengine.EnvEngine+"=")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err = command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != mockengine.ExitUsage ||
		!strings.Contains(stderr.String(), "2.1.238") {
		t.Fatalf(
			"stray name: err=%v stderr=%q, want exit %d naming the basename",
			err,
			stderr.String(),
			mockengine.ExitUsage,
		)
	}
}

// TestRunMockAnswersInProcess drives the exec shell's body in this process, the
// door the built-binary test reaches only through a child built without -cover.
func TestRunMockAnswersInProcess(t *testing.T) {
	scenario := filepath.Join(t.TempDir(), "scenario.json")
	if err := (mockengine.Scenario{Version: "9.9.9 (Fixture)"}).Write(scenario); err != nil {
		t.Fatal(err)
	}
	env := func(key string) string {
		if key == mockengine.EnvScenario {
			return scenario
		}
		return ""
	}

	var stdout, stderr bytes.Buffer
	code := runMock("claude", []string{"--version"}, strings.NewReader(""), &stdout, &stderr, env)
	if code != 0 || stdout.String() != "9.9.9 (Fixture)\n" || stderr.Len() != 0 {
		t.Fatalf(
			"claude --version: code=%d stdout=%q stderr=%q, want 0 and the version line",
			code,
			stdout.String(),
			stderr.String(),
		)
	}

	// An argv[0] that names no engine is refused with the usage code, never
	// silently one engine.
	stdout.Reset()
	stderr.Reset()
	code = runMock("2.1.238", []string{"--version"}, strings.NewReader(""), &stdout, &stderr, env)
	if code != mockengine.ExitUsage || stdout.Len() != 0 || !strings.Contains(stderr.String(), "2.1.238") {
		t.Fatalf("stray name: code=%d stdout=%q stderr=%q, want exit %d naming the basename",
			code, stdout.String(), stderr.String(), mockengine.ExitUsage)
	}
}

func TestHangupReleasesTheJailSeat(t *testing.T) {
	root := t.TempDir()
	binary, err := testjail.MockEngineBinary("../..", root)
	if err != nil {
		t.Fatalf("go build: %v", err)
	}
	for _, signal := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			home := t.TempDir()
			link := filepath.Join(home, "claude")
			if err := os.Symlink(binary, link); err != nil {
				t.Fatal(err)
			}
			busy := 0
			procRoot := filepath.Join(home, "proc")
			scenario := filepath.Join(home, "scenario.json")
			if err := (mockengine.Scenario{
				Version: "9.9.9 (Fixture)", SessionID: "b1111111-1111-4111-8111-111111111111",
				BusyMS: &busy, Jail: mockengine.Jail{ProcRoot: procRoot},
			}).Write(scenario); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(link)
			command.Dir = home
			command.Env = append(os.Environ(), mockengine.EnvScenario+"="+scenario,
				"CLAUDE_CONFIG_DIR="+filepath.Join(home, "config"), "HOME="+home)
			stdin, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stdin.Close() }()
			var stderr bytes.Buffer
			command.Stdout, command.Stderr = io.Discard, &stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan struct{})
			var waitErr error
			go func() {
				waitErr = command.Wait()
				close(exited)
			}()
			t.Cleanup(func() {
				_ = command.Process.Kill()
				<-exited
			})
			entry := filepath.Join(procRoot, strconv.Itoa(command.Process.Pid))
			deadline := time.NewTimer(10 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for {
				if _, err := os.Stat(entry); err == nil {
					break
				} else if !os.IsNotExist(err) {
					t.Fatalf("read jail seat: %v", err)
				}
				select {
				case <-exited:
					t.Fatalf("mock exited before binding its seat: %v: %s", waitErr, stderr.String())
				case <-deadline.C:
					t.Fatalf("mock did not bind jail seat %s within 10 s", entry)
				case <-tick.C:
				}
			}
			if err := command.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			deadline.Reset(10 * time.Second)
			select {
			case <-exited:
			case <-deadline.C:
				t.Fatal("mock did not exit within 10 s of the signal")
			}
			if _, err := os.Stat(entry); !os.IsNotExist(err) {
				t.Fatalf("jail seat remains after %s: stat=%v exit=%v stderr=%q", signal, err, waitErr, stderr.String())
			}
			if waitErr != nil {
				t.Fatalf("mock exit after %s: %v: %s", signal, waitErr, stderr.String())
			}
		})
	}
}
