package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/shared"
)

// The reaper KILLS things, so its fixtures run against real tmux servers on
// scratch sockets inside a jailed TMUX_TMPDIR. Nothing here can see a live
// fleet socket: the sweep only ever reads the directory the jail points it at.

// reapJail builds a short-pathed jail. Short matters: a tmux socket path is
// capped near 104 bytes, and t.TempDir() spends most of that on the test name,
// where the failure surfaces as an unrelated-looking tmux error.
func reapJail(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("", "rp")
	if err != nil {
		t.Fatalf("create jail root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	socketDir := filepath.Join(root, "t", "tmux-"+strconv.Itoa(os.Getuid()))
	accountRoot := filepath.Join(root, "home", ".cc", "1", "projects")
	for _, directory := range []string{
		socketDir,
		filepath.Join(root, "sid"),
		accountRoot,
		filepath.Join(root, "codex"),
		filepath.Join(root, "bin"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	// TMUX_TMPDIR and PFM_TMUX_DIR must agree: the probe addresses a
	// socket by -L (which resolves through TMUX_TMPDIR) and the kill by -S
	// (which joins PFM_TMUX_DIR), and a jail where those two disagree
	// tests nothing at all.
	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "t"))
	t.Setenv("PFM_TMUX_DIR", socketDir)
	t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))
	t.Setenv("PFM_HOME", filepath.Join(root, "home"))
	t.Setenv("PFM_CLAUDE_ROOTS", accountRoot)
	t.Setenv("PFM_CODEX_ROOT", filepath.Join(root, "codex"))
	t.Setenv("PFM_DB", filepath.Join(root, "fleet.db"))
	t.Setenv("PFM_SHARED_DB", filepath.Join(root, "shared.db"))
	t.Setenv("PFM_TMUX_CONF", "/dev/null")
	// The reaper reads the REAL /proc here on purpose: the jail's panes are
	// real processes, and the non-chat guard is only proved by a real process
	// tree. Reading /proc mutates nothing.
	t.Setenv("PFM_PROC_ROOT", "/proc")
	// A stub `claude agents --json` keeps the busy probe answering. Without an
	// answer the sweep fails closed and would kill nothing — which would make
	// this fixture pass for the wrong reason.
	stub := filepath.Join(root, "bin", "claude")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho '[]'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+":"+os.Getenv("PATH"))
	t.Setenv("TMUX", "")
	return root
}

func reapSocketDir(root string) string {
	return filepath.Join(root, "t", "tmux-"+strconv.Itoa(os.Getuid()))
}

// startShellPane brings up a scratch server whose pane is a real shell — the
// shape a chat socket has once its chat has exited.
func startShellPane(t *testing.T, root, socket string) {
	t.Helper()
	path := filepath.Join(reapSocketDir(root), socket)
	command := exec.Command(
		"tmux", "-f", "/dev/null", "-S", path,
		"new-session", "-d", "-s", socket, "bash", "--norc",
	)
	command.Env = append(os.Environ(), "TMUX=")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start jail server %q: %v: %s", socket, err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", path, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		_ = kill.Run()
	})
}

// runInPane types a command into a pane and waits for tmux to report it
// running, so the fixture never races the shell it just fed.
func runInPane(t *testing.T, root, socket, line, wantCommand string) {
	t.Helper()
	path := filepath.Join(reapSocketDir(root), socket)
	send := exec.Command("tmux", "-S", path, "send-keys", "-t", socket, line, "Enter")
	send.Env = append(os.Environ(), "TMUX=")
	if output, err := send.CombinedOutput(); err != nil {
		t.Fatalf("send %q: %v: %s", line, err, output)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		list := exec.Command(
			"tmux", "-S", path, "list-panes", "-a", "-F", "#{pane_current_command}",
		)
		list.Env = append(os.Environ(), "TMUX=")
		output, err := list.Output()
		if err == nil && strings.Contains(string(output), wantCommand) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pane never started %q", wantCommand)
}

func reapServerAlive(root, socket string) bool {
	command := exec.Command(
		"tmux", "-S", filepath.Join(reapSocketDir(root), socket), "list-panes", "-a",
	)
	command.Env = append(os.Environ(), "TMUX=")
	return command.Run() == nil
}

// A /chat:branch seat has no transcript until its first prompt, so the usual
// Claude busy proof is unavailable. Its durable branch-seat marker is the
// evidence that an untouched, unattached fork is intentionally reapable.
func TestReapClassifiesAndClearsAnUntouchedDetachedFork(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := reapJail(t)
	const socket = "cc-1800000000-42-9"
	startShellPane(t, root, socket)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	state := shared.Open(context.Background(), resolved)
	if state.Degraded() != nil {
		t.Fatal(state.Degraded())
	}
	if err := state.SetMeta(
		context.Background(), "branch-seat:"+socket, "parent-fixture", time.Now().Unix(),
	); err != nil {
		_ = state.Close()
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"reap"}, &stdout, &stderr); code != 0 {
		t.Fatalf("reap dry run rc=%d: %s", code, stderr.String())
	}
	if row := reapRow(t, stdout.String(), socket); !strings.Contains(row, "fork") {
		t.Fatalf("untouched fork was not classified distinctly: %q\n%s", row, stdout.String())
	}
	if !reapServerAlive(root, socket) {
		t.Fatal("dry run killed the detached fork")
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"reap", "--apply"}, &stdout, &stderr); code != 0 {
		t.Fatalf("reap --apply rc=%d: %s", code, stderr.String())
	}
	if reapServerAlive(root, socket) {
		t.Fatalf("apply left untouched fork alive:\n%s", stdout.String())
	}
	state = shared.Open(context.Background(), resolved)
	defer func() {
		if err := state.Close(); err != nil {
			t.Errorf("close state: %v", err)
		}
	}()
	if _, found, err := state.Meta(context.Background(), "branch-seat:"+socket); err != nil || found {
		t.Fatalf("branch marker after reap found=%t err=%v", found, err)
	}
}

// TestReapNeverKillsASocketHostingNonChatWork is the rule that cost a chat
// once already: a socket also carrying somebody's dev servers is not an
// orphan, however idle its chat looks. The twin socket in the same sweep
// proves the guard is a guard and not a refusal to reap anything.
func TestReapNeverKillsASocketHostingNonChatWork(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := reapJail(t)
	const hosting = "cc-1800000001-42-1"
	const orphan = "cc-1800000002-42-2"
	startShellPane(t, root, hosting)
	startShellPane(t, root, orphan)
	runInPane(t, root, hosting, "sleep 600", "sleep")

	// Both sockets carry a breadcrumb: without one a claude socket is
	// busy-unknown and skipped, which would kill the difference under a rule
	// that is not the one under test.
	for _, socket := range []string{hosting, orphan} {
		crumb := filepath.Join(root, "sid", socket)
		transcript := filepath.Join(root, "home", ".cc", "1", "projects", socket+".jsonl")
		if err := os.WriteFile(crumb, []byte(transcript+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"reap"}, &stdout, &stderr); code != 0 {
		t.Fatalf("reap dry run rc = %d: %s", code, stderr.String())
	}
	report := stdout.String()
	if !strings.Contains(report, hosting+" ") ||
		!strings.Contains(reapRow(t, report, hosting), "hosts") {
		t.Fatalf("the hosting socket was not protected:\n%s", report)
	}
	if !strings.Contains(reapRow(t, report, orphan), "orph") {
		t.Fatalf("the idle socket was not classified reapable:\n%s", report)
	}
	if !reapServerAlive(root, hosting) || !reapServerAlive(root, orphan) {
		t.Fatal("the dry run killed something")
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"reap", "--apply"}, &stdout, &stderr); code != 0 {
		t.Fatalf("reap --apply rc = %d: %s", code, stderr.String())
	}
	if !reapServerAlive(root, hosting) {
		t.Fatalf("--apply killed a socket hosting live work:\n%s", stdout.String())
	}
	if reapServerAlive(root, orphan) {
		t.Fatalf("--apply left the orphan running:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(reapSocketDir(root), orphan)); !os.IsNotExist(err) {
		t.Fatalf("the reaped socket file outlived its server: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sid", orphan)); !os.IsNotExist(err) {
		t.Fatalf("the reaped chat's crumb still points at a dead server: %v", err)
	}
}

// A chat writing its transcript right now is working, whatever the busy
// snapshot said a moment ago.
func TestReapKeepsAChatWhoseTranscriptIsBeingWritten(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := reapJail(t)
	const socket = "cc-1800000003-42-3"
	startShellPane(t, root, socket)
	transcript := filepath.Join(root, "home", ".cc", "1", "projects", "live.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	crumb := filepath.Join(root, "sid", socket)
	if err := os.WriteFile(crumb, []byte(transcript+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"reap", "--apply"}, &stdout, &stderr); code != 0 {
		t.Fatalf("reap rc = %d: %s", code, stderr.String())
	}
	if !reapServerAlive(root, socket) {
		t.Fatalf("a chat mid-turn was reaped:\n%s", stdout.String())
	}
	if !strings.Contains(reapRow(t, stdout.String(), socket), "active") {
		t.Fatalf("the working chat was not reported active:\n%s", stdout.String())
	}
}

// A socket file with no server behind it is only a corpse once it is old
// enough that a server cannot still be starting up behind it.
func TestReapLeavesAYoungEmptySocketAlone(t *testing.T) {
	root := reapJail(t)
	const socket = "cc-1800000004-42-4"
	path := filepath.Join(reapSocketDir(root), socket)
	// A crashed tmux server leaves a socket FILE with nothing listening
	// behind it. SetUnlinkOnClose(false) keeps the file after the listener
	// goes away — a listener that stays up would accept the probe's
	// connection and never answer it, which is a different fault entirely.
	address, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		t.Fatalf("resolve scratch socket: %v", err)
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatalf("create scratch socket: %v", err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatalf("close scratch socket: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"reap", "--apply"}, &stdout, &stderr); code != 0 {
		t.Fatalf("reap rc = %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a socket younger than an hour was removed: %v", err)
	}
	if !strings.Contains(reapRow(t, stdout.String(), socket), "SKIP") {
		t.Fatalf("young empty socket not reported as skipped:\n%s", stdout.String())
	}

	// The same socket, aged past the corpse threshold, IS swept.
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := run([]string{"reap", "--apply"}, &stdout, &stderr); code != 0 {
		t.Fatalf("reap rc = %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("an hours-old empty socket survived the sweep: %v", err)
	}
}

// A sweep that could not do what it said must not report success: a removal
// that genuinely fails has to drive the command's own exit code non-zero, or
// a script chaining on that exit code sees success where a corpse was left
// behind untouched.
//
// This exact proof used to live here as a full-binary test, forcing the
// failure with a read-only TmuxDir. That trick is a permission-bit check,
// and this repo's own isolated fence runs the suite as root, where
// permission bits are advisory — root's os.Remove succeeds right through a
// chmod 0500 directory, so the "failure" never happened and the test only
// ever proved itself on a machine that was not root. The same source branch
// (internal/reap/runner.go's ActionRemoveSocketFile os.Remove failure) is
// proven root-proof instead in internal/reap/reap_test.go, using a
// non-empty directory in the corpse's place — os.Remove on a non-empty
// directory returns ENOTEMPTY for every uid, root included — alongside
// TestApplyMarksADecisionFailedWhenKillServerErrors, which was already
// root-proof (a fake killServer returning an error, no filesystem trick at
// all). Reaching that branch through the REAL binary and real tmux without
// either a permission check or a timing race against a live server was not
// achievable within the test-files-only boundary; see the audit report for
// the mechanisms considered and why each was rejected.

// reapRow returns the report line for one socket.
func reapRow(t *testing.T, report, socket string) string {
	t.Helper()
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, socket+" ") {
			return line
		}
	}
	t.Fatalf("no row for %s in:\n%s", socket, report)
	return ""
}
