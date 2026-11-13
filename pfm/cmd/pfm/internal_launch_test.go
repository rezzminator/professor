package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

type synchronizedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(content []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.b.Write(content)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.b.String()
}

func TestInternalLaunchPrintExecsDirectlyWithoutTmux(t *testing.T) {
	root := jailTest(t)
	realBinary := filepath.Join(root, "bin", "claude")
	writeExecutable(t, realBinary, "#!/bin/sh\nprintf 'direct:%s\\n' \"$*\"\nexit 7\n")
	command := exec.Command(os.Args[0], "-test.run=^TestInternalLaunchPrintHelper$")
	command.Env = append(os.Environ(),
		"PFM_TEST_LAUNCH_PRINT_HELPER=1",
		"PFM_TEST_LAUNCH_REAL="+realBinary,
	)
	output, err := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("print helper error=%v output=%q, want status 7", err, output)
	}
	if got, want := string(output), "direct:-p hello\n"; got != want {
		t.Fatalf("direct output=%q, want %q", got, want)
	}
	entries, err := os.ReadDir(os.Getenv("PFM_TMUX_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("print launch created tmux entries: %v", entries)
	}
}

func TestInternalLaunchPrintHelper(t *testing.T) {
	if os.Getenv("PFM_TEST_LAUNCH_PRINT_HELPER") != "1" {
		t.Skip("internal launch print fixture runs only as a helper process")
	}
	code := run(
		[]string{"internal", "launch", "--real", os.Getenv("PFM_TEST_LAUNCH_REAL"), "--", "-p", "hello"},
		os.Stdout,
		os.Stderr,
	)
	os.Exit(code)
}

func TestInternalLaunchFreshAndExplicitRecordBeforeTmux(t *testing.T) {
	for _, scenario := range []struct {
		name, explicit string
		arguments      []string
	}{
		{name: "fresh"},
		{name: "explicit", explicit: "22222222-2222-4222-8222-222222222222", arguments: []string{"--session-id", "22222222-2222-4222-8222-222222222222"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := jailTest(t)
			t.Setenv("TMUX", "")
			t.Setenv("PFM_LAUNCH_PASSTHROUGH", "")
			bin := filepath.Join(root, "bin")
			argumentsPath := filepath.Join(root, "tmux-arguments")
			writeExecutable(
				t,
				filepath.Join(bin, "tmux"),
				"#!/bin/sh\ncase \" $* \" in\n *' new-session '*) printf '%s\\n' \"$*\" > \"$CC_TMUX_ARGUMENTS\"; printf 'fixture tmux refused' >&2; exit 9;;\n *' display-message '*) echo \"$PPID\";;\n *' kill-server '*) exit 0;;\n esac\n",
			)
			realBinary := filepath.Join(bin, "claude")
			writeExecutable(t, realBinary, "#!/bin/sh\nexit 0\n")
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("CC_TMUX_ARGUMENTS", argumentsPath)
			var stdout, stderr bytes.Buffer
			arguments := append(
				[]string{"internal", "launch", "--real", realBinary, "--cwd", root, "--"},
				scenario.arguments...)
			if code := run(
				arguments,
				&stdout,
				&stderr,
			); code != 1 ||
				!strings.Contains(stderr.String(), "fixture tmux refused") {
				t.Fatalf("launcher code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			content, err := os.ReadFile(argumentsPath)
			if err != nil {
				t.Fatal(err)
			}
			line := string(content)
			matches := regexp.MustCompile(`'--session-id' '([^']+)'`).FindStringSubmatch(line)
			if len(matches) != 2 || strings.Count(line, "--session-id") != 1 {
				t.Fatalf("tmux command has no single session id: %q", line)
			}
			id := matches[1]
			if scenario.explicit != "" && id != scenario.explicit {
				t.Fatalf("session id=%q, want %q", id, scenario.explicit)
			}
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			launches, err := fleetdb.OpenLaunches(context.Background(), values)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := launches.Close(); err != nil {
					t.Error(err)
				}
			}()
			record, err := launches.LaunchFor(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if record.Account != 1 || !record.Cache1H || string(record.Engine) != "cc" {
				t.Fatalf("launcher record=%+v", record)
			}
		})
	}
}

func TestInternalLaunchTmuxStartFailureIsLoudAndNeverFallsBack(t *testing.T) {
	root := jailTest(t)
	bin := filepath.Join(root, "broken-bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "bare-claude-ran")
	writeExecutable(t, filepath.Join(bin, "tmux"), "#!/bin/sh\nprintf 'fixture tmux refused' >&2\nexit 9\n")
	realBinary := filepath.Join(bin, "claude")
	writeExecutable(t, realBinary, "#!/bin/sh\n: > \"$CC_BARE_MARKER\"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CC_BARE_MARKER", marker)

	var stdout, stderr bytes.Buffer
	code := run([]string{"internal", "launch", "--real", realBinary, "--", "--resume", "fixture"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "create chat server") ||
		!strings.Contains(stderr.String(), "fixture tmux refused") {
		t.Fatalf("tmux failure code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("failed tmux start fell back to bare Claude: %v", err)
	}
}

func TestDoctorReportsClaudeLauncherMissingDisplacedAndOK(t *testing.T) {
	root := jailTest(t)
	home := filepath.Join(root, "home")
	testjail.PinClaudeAsk(t, home)
	canonical := filepath.Join(home, ".local", "bin", "claude")
	managed := filepath.Join(home, ".local", "share", "pfm", "install", "bin", "claude")
	native := filepath.Join(home, ".local", "share", "claude", "versions", "fixture")
	if err := os.Remove(canonical); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(managed); err != nil {
		t.Fatal(err)
	}

	doctor := func() (int, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := run([]string{"doctor"}, &stdout, &stderr)
		if stderr.Len() != 0 {
			t.Fatalf("doctor stderr=%q", stderr.String())
		}
		return code, stdout.String()
	}
	code, output := doctor()
	if code != 3 || !strings.Contains(output, "doctor: launcher: missing — run pfm install") {
		t.Fatalf("missing launcher doctor code=%d:\n%s", code, output)
	}

	writeExecutable(t, managed, "#!/bin/sh\nexit 0\n")
	if err := os.Symlink(managed, canonical); err != nil {
		t.Fatal(err)
	}
	code, output = doctor()
	if code != 0 || !strings.Contains(output, "doctor: launcher: ok") {
		t.Fatalf("healthy launcher doctor code=%d:\n%s", code, output)
	}

	if err := os.Remove(canonical); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, native, "#!/bin/sh\nexit 0\n")
	if err := os.Symlink(native, canonical); err != nil {
		t.Fatal(err)
	}
	code, output = doctor()
	if code != 3 || !strings.Contains(output, "doctor: launcher: DISPLACED by "+native+" — run pfm install") {
		t.Fatalf("displaced launcher doctor code=%d:\n%s", code, output)
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}
