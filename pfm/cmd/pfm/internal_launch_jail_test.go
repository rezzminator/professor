package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

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
	t.Setenv(paths.EnvProcRoot, paths.SystemProcRoot())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "poison-parent")
	t.Setenv("CLAUDECODE", "poison-parent")
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "poison-parent")
	t.Setenv("ANTHROPIC_BASE_URL", "https://poison.invalid")
	if err := os.MkdirAll(filepath.Join(root, "caller-config"), 0o700); err != nil {
		t.Fatal(err)
	}
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
  printf 'ttl=%s\n' "${CACHE_LIVE_CONTROL_MAIN_TTL-unset}"
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
		"argv=--remote-control --resume fixture-id --dangerously-skip-permissions --settings {",
		"ttl=1h",
		"config=" + filepath.Join(root, "caller-config"),
		"force=unset", "sid=unset", "child=unset", "endpoint=unset",
	} {
		if !strings.Contains(string(proof), want) {
			t.Fatalf("launch evidence missing %q:\n%s", want, proof)
		}
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
	record, err := launches.LaunchFor(context.Background(), "fixture-id")
	if err != nil {
		t.Fatal(err)
	}
	if record.SessionID != "fixture-id" || record.Account != 1 || !record.Cache1H {
		t.Fatalf("managed launcher record=%+v", record)
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
