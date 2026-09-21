package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/doctor"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func clearRetiredHarvesterEnv(t *testing.T) {
	t.Helper()
	for _, retired := range doctor.RetiredHarvesterEnv {
		t.Setenv(retired.Name, "")
	}
}

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

func TestInternalLaunchNoTTYCreatesListedSessionAndPropagatesExit(t *testing.T) {
	root := jailTest(t)
	// The test calls run from a goroutine, so inheriting a managed parent
	// socket would take the pass-through branch and syscall.Exec the fixture
	// over the entire Go test process. Keep this launch outside tmux so the
	// bounded readiness wait below remains alive to report fixture failures.
	t.Setenv("TMUX", "")
	t.Setenv("PFM_LAUNCH_PASSTHROUGH", "")
	socket := "cc-1700000000-123-456"
	tmuxDir := filepath.Join(root, "tmux-"+fmt.Sprint(os.Getuid()))
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_TMUX_DIR", tmuxDir)
	t.Setenv("PFM_TEST_FRESH_SOCKET", socket)
	t.Setenv("PFM_PROC_ROOT", "/proc")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "poison-parent")
	t.Setenv("CLAUDECODE", "poison-parent")
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "poison-parent")
	t.Setenv("ANTHROPIC_BASE_URL", "https://poison.invalid")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "caller-config"))

	ready := filepath.Join(root, "ready")
	release := filepath.Join(root, "release")
	evidence := filepath.Join(root, "evidence")
	transcript := filepath.Join(root, "claude", "project", "11111111-1111-4111-8111-111111111111.jsonl")
	realBinary := filepath.Join(root, "bin", "claude")
	writeExecutable(t, realBinary, `#!/bin/sh
set -eu
/bin/bash -c 'exec -a claude /bin/sleep 30' &
claude_child=$!
trap 'kill "$claude_child" 2>/dev/null || true; wait "$claude_child" 2>/dev/null || true' EXIT
socket=$(basename "${TMUX%%,*}")
mkdir -p "$(dirname "$CC_LAUNCH_TRANSCRIPT")" "$PFM_SID_DIR"
printf '%s\n' '{"type":"user","cwd":"/work","message":{"content":"fixture"}}' > "$CC_LAUNCH_TRANSCRIPT"
printf '%s' "$CC_LAUNCH_TRANSCRIPT" > "$PFM_SID_DIR/$socket"
{
  printf 'tmux=%s\n' "$socket"
  printf 'argv=%s\n' "$*"
  printf 'config=%s\n' "${CLAUDE_CONFIG_DIR-unset}"
  printf 'force=%s\n' "${FORCE_PROMPT_CACHING_5M-unset}"
  printf 'sid=%s\n' "${CLAUDE_CODE_SESSION_ID-unset}"
  printf 'child=%s\n' "${CLAUDE_CODE_CHILD_SESSION-unset}"
  printf 'endpoint=%s\n' "${ANTHROPIC_BASE_URL-unset}"
} > "$CC_LAUNCH_EVIDENCE"
: > "$CC_LAUNCH_READY"
while [ ! -e "$CC_LAUNCH_RELEASE" ]; do sleep 0.02; done
exit 3
`)
	t.Setenv("CC_LAUNCH_READY", ready)
	t.Setenv("CC_LAUNCH_RELEASE", release)
	t.Setenv("CC_LAUNCH_EVIDENCE", evidence)
	t.Setenv("CC_LAUNCH_TRANSCRIPT", transcript)

	var stdout, stderr synchronizedBuffer
	done := make(chan int, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- run([]string{"internal", "launch", "--real", realBinary, "--cwd", filepath.Join(root, "work"), "--", "--resume", "fixture-id", "--dangerously-skip-permissions"}, &stdout, &stderr)
	}()
	// A failed assertion must not strand the seven-day launcher wait or its
	// fixture process. The original test leaked both when ls or readiness
	// failed, which turned one failure into an 11-minute package hang.
	t.Cleanup(func() {
		select {
		case <-finished:
			return
		default:
		}
		_ = os.WriteFile(release, []byte("cleanup\n"), 0o600)
		kill := exec.Command("tmux", "-S", filepath.Join(tmuxDir, socket), "kill-server")
		kill.Env = os.Environ()
		_ = kill.Run()
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
		}
	})
	waitForPath(t, ready)

	var listOut, listErr bytes.Buffer
	liveDeadline := time.Now().Add(10 * time.Second)
	for {
		listOut.Reset()
		listErr.Reset()
		if code := runWithTestTimeout(t, 10*time.Second, "pfm ls --plain", func() int {
			return run([]string{"ls", "--plain"}, &listOut, &listErr)
		}); code != 0 {
			t.Fatalf("pfm ls --plain code=%d stderr=%q", code, listErr.String())
		}
		if strings.Contains(listOut.String(), "● fixture") {
			break
		}
		if time.Now().After(liveDeadline) {
			t.Fatalf("pfm ls --plain did not observe the fake Claude as live before timeout:\n%s", listOut.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	var tsvOut, tsvErr bytes.Buffer
	if code := runWithTestTimeout(t, 10*time.Second, "pfm ls --tsv", func() int {
		return run([]string{"ls", "--tsv"}, &tsvOut, &tsvErr)
	}); code != 0 {
		t.Fatalf("pfm ls --tsv code=%d stderr=%q", code, tsvErr.String())
	}
	if !strings.Contains(tsvOut.String(), "\t"+socket+"\n") {
		t.Fatalf("live TSV omitted launch socket %q:\n%s", socket, tsvOut.String())
	}
	if err := os.WriteFile(release, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 3 {
			t.Fatalf(
				"internal launch code=%d, want fake Claude status 3; stdout=%q stderr=%q",
				code,
				stdout.String(),
				stderr.String(),
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("internal launch did not return after its pane exited")
	}
	if got, want := stdout.String(), "pfm launch: "+socket+" "+socket+"\n"; got != want {
		t.Fatalf("stdout=%q, want one socket line %q", got, want)
	}
	if stderr.String() != "" {
		t.Fatalf("stderr=%q, want empty", stderr.String())
	}
	proof, err := os.ReadFile(evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"tmux=" + socket,
		"argv=--resume fixture-id --dangerously-skip-permissions --settings " + pfmengine.OutputStyleDefaultSettings,
		"config=" + filepath.Join(root, "caller-config"),
		"force=1", "sid=unset", "child=unset", "endpoint=unset",
	} {
		if !strings.Contains(string(proof), want+"\n") {
			t.Fatalf("launch evidence missing %q:\n%s", want, proof)
		}
	}
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
	clearRetiredHarvesterEnv(t) // golden doctor output must not depend on an ambient retired harvester variable
	root := jailTest(t)
	home := filepath.Join(root, "home")
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
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func waitForPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func runWithTestTimeout(t *testing.T, timeout time.Duration, name string, run func() int) int {
	t.Helper()
	result := make(chan int, 1)
	go func() { result <- run() }()
	select {
	case code := <-result:
		return code
	case <-time.After(timeout):
		t.Fatalf("%s did not return within %s", name, timeout)
		return -1
	}
}
