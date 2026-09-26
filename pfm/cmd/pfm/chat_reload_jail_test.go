package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// reload is a public chat operation; keep the contract pinned at the CLI
// boundary. `swap` was the pre-port spelling and is gone — dispatch must say
// so rather than quietly accepting a name nothing documents.
func TestChatReloadAcceptsCacheOnlyRequest(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	code := runChat(
		[]string{"reload", "--1h", "on"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	if code == 2 && strings.Contains(stderr.String(), `unknown command "reload"`) {
		t.Fatalf("reload dispatch is still missing: rc=%d stderr=%q", code, stderr.String())
	}
}

func TestChatReloadHelpIsPublicAndSuccessful(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	code := runChat(
		[]string{"reload", "--help"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	if code != 0 || !strings.Contains(stdout.String(), "usage: pfm chat reload") {
		t.Fatalf("reload help rc=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// The retired `swap` alias must be refused by name. A dispatch that still
// answered it would keep a second public spelling alive that no help text,
// card, or doc mentions.
func TestChatSwapAliasIsRetired(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	code := runChat(
		[]string{"swap", "--help"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	if code != 2 || !strings.Contains(stderr.String(), `unknown command "swap"`) {
		t.Fatalf(
			"retired swap alias still dispatches: rc=%d stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestChatReloadRefusesAnOpenSelectorOnAProbeSocket(t *testing.T) {
	jailTest(t)
	t.Setenv("PFM_RELOAD_DELAY_MS", "1")
	t.Setenv("PFM_RELOAD_POLL_MS", "1")
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	socket := probeReloadSocket(t, "selector")
	server := exec.Command(
		"tmux", "-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", "probe",
		"printf '❯ 1. choose\\n'; sleep 120",
	)
	server.Env = append(server.Environ(), "TMUX=")
	if output, err := server.CombinedOutput(); err != nil {
		t.Fatalf("start probe socket: %v: %s", err, output)
	}
	cleanupProbeReloadSocket(t, socket)
	paneOutput, err := exec.Command("tmux", "-S", socket, "list-panes", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatalf("read probe pane: %v", err)
	}
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(resolved.SIDDir, 0o700); err != nil {
		t.Fatal(err)
	}
	crumb := filepath.Join(
		resolved.SIDDir,
		filepath.Base(socket)+"."+strings.TrimSpace(string(paneOutput)),
	)
	if err := os.WriteFile(
		crumb,
		[]byte(filepath.Join(t.TempDir(), "22222222-2222-4222-8222-222222222222.jsonl")+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runChatReloadWorker([]string{"--sock", socket, "--1h", "on"}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "open selector menu") {
		t.Fatalf("reload selector gate rc=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if output, err := exec.Command("tmux", "-S", socket, "list-panes", "-F", "#{pane_current_command}").
		Output(); err != nil ||
		strings.TrimSpace(string(output)) == "" {
		t.Fatalf("selector gate lost the pane: err=%v output=%q", err, output)
	}
}

func TestChatReloadSchedulesADetachedWorker(t *testing.T) {
	root := jailTest(t)
	configPath := writeConfigFixture(t, root, `{
  "version": 1,
  "accounts": [
    {"id": 1, "configDir": "`+filepath.Join(root, "account-1")+`"},
    {"id": 2, "configDir": "`+filepath.Join(root, "account-2")+`"}
  ]
}`)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	socket := probeReloadSocket(t, "schedule")
	server := exec.Command(
		"tmux", "-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", "probe",
		"sleep 120",
	)
	server.Env = append(server.Environ(), "TMUX=")
	if output, err := server.CombinedOutput(); err != nil {
		t.Fatalf("start probe socket: %v: %s", err, output)
	}
	cleanupProbeReloadSocket(t, socket)
	old := startReloadWorker
	t.Cleanup(func() { startReloadWorker = old })
	var workerArgs []string
	detached := false
	startReloadWorker = func(argv []string, opts deps.StartOptions) error {
		workerArgs = append([]string(nil), argv...)
		detached = opts.Detach && opts.Stdout != nil && opts.Stdout == opts.Stderr
		return nil
	}
	var stdout, stderr bytes.Buffer
	if code := run(
		[]string{"--config", configPath, "chat", "reload", "2", "--sock", socket, "--1h", "on"},
		&stdout,
		&stderr,
	); code != 0 {
		t.Fatalf("schedule rc=%d stderr=%q", code, stderr.String())
	}
	joined := strings.Join(workerArgs, "\x00")
	if !strings.Contains(joined, "\x00--config\x00") ||
		!strings.Contains(joined, "\x00internal\x00reload-run\x002\x00") {
		t.Fatalf("worker argv = %q", workerArgs)
	}
	if !strings.Contains(stdout.String(), "reload scheduled") {
		t.Fatalf("schedule receipt = %q", stdout.String())
	}
	if !detached {
		t.Fatal("reload worker retained the caller's process group or stdio pipes")
	}
}

// TestChatReloadHandsTheWorkerAnExplicitSockAndPane is the regression for the
// detached-worker identity bug: a caller with NO --sock of its own is found
// through ambient tmux identity ($TMUX / $TMUX_PANE, exactly like a real chat
// running inside tmux). The scheduler must hand the worker that resolved
// (socket, pane) explicitly via --sock/--pane — a Setsid-detached worker,
// reparented off any tmux ancestor, has neither $TMUX nor a process chain
// left to re-derive it from (this is the couldn't-identify-this-chat bug a
// bg-spare-served chat hit in production).
func TestChatReloadHandsTheWorkerAnExplicitSockAndPane(t *testing.T) {
	root := jailTest(t)
	configPath := writeConfigFixture(t, root, `{
  "version": 1,
  "accounts": [
    {"id": 1, "configDir": "`+filepath.Join(root, "account-1")+`"},
    {"id": 2, "configDir": "`+filepath.Join(root, "account-2")+`"}
  ]
}`)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	socket := probeReloadSocket(t, "ambient")
	server := exec.Command(
		"tmux", "-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", "probe",
		"sleep 120",
	)
	server.Env = append(server.Environ(), "TMUX=")
	if output, err := server.CombinedOutput(); err != nil {
		t.Fatalf("start probe socket: %v: %s", err, output)
	}
	cleanupProbeReloadSocket(t, socket)
	paneOutput, err := exec.Command("tmux", "-S", socket, "list-panes", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatalf("read probe pane: %v", err)
	}
	pane := strings.TrimSpace(string(paneOutput))

	// The calling chat is identified AMBIENTLY here, exactly as a real caller
	// inside tmux is: $TMUX names the socket, $TMUX_PANE names the pane, and
	// no --sock is typed on the command line.
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", pane)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")

	old := startReloadWorker
	t.Cleanup(func() { startReloadWorker = old })
	var workerArgs []string
	startReloadWorker = func(argv []string, _ deps.StartOptions) error {
		workerArgs = append([]string(nil), argv...)
		return nil
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", configPath, "chat", "reload", "2", "--1h", "on"}, &stdout, &stderr); code != 0 {
		t.Fatalf("schedule rc=%d stderr=%q", code, stderr.String())
	}
	joined := strings.Join(workerArgs, "\x00")
	if !strings.Contains(joined, "\x00--sock\x00"+socket+"\x00") {
		t.Fatalf("worker argv missing the resolved --sock %s: %q", socket, workerArgs)
	}
	if !strings.HasSuffix(joined, "\x00--pane\x00"+pane) {
		t.Fatalf("worker argv missing the resolved --pane %s: %q", pane, workerArgs)
	}
}

// TestChatReloadWithExplicitPaneOnAMultiPaneServerResolves is the regression
// for the scheduler's own hardcoded pane="": validateReloadArgs accepted a
// caller-typed --pane, but runChatReloadWithRuntime's own call into
// reloadTarget passed "" no matter what, so `pfm chat reload --sock <server>
// --pane <live pane>` was refused with "has multiple panes — run reload
// inside the chat instead" even though the caller had disambiguated it. The
// scheduler must now read reloadPaneArgument(args) and pass it through, and —
// since the caller's own args already carry a --pane — must NOT also append
// its own, which would leave two --pane flags in the worker's argv.
func TestChatReloadWithExplicitPaneOnAMultiPaneServerResolves(t *testing.T) {
	root := jailTest(t)
	configPath := writeConfigFixture(t, root, `{
  "version": 1,
  "accounts": [
    {"id": 1, "configDir": "`+filepath.Join(root, "account-1")+`"},
    {"id": 2, "configDir": "`+filepath.Join(root, "account-2")+`"}
  ]
}`)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	socket := probeReloadSocket(t, "explicit-multi")
	server := exec.Command(
		"tmux", "-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", "probe",
		"sleep 120",
	)
	server.Env = append(server.Environ(), "TMUX=")
	if output, err := server.CombinedOutput(); err != nil {
		t.Fatalf("start probe socket: %v: %s", err, output)
	}
	cleanupProbeReloadSocket(t, socket)
	if output, err := exec.Command(
		"tmux", "-S", socket, "new-window", "-d", "-n", "second", "sleep 120",
	).CombinedOutput(); err != nil {
		t.Fatalf("start second probe pane: %v: %s", err, output)
	}
	paneOutput, err := exec.Command("tmux", "-S", socket, "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatalf("read probe panes: %v", err)
	}
	panes := strings.Fields(strings.TrimSpace(string(paneOutput)))
	if len(panes) != 2 {
		t.Fatalf("probe server has %d panes, want 2: %q", len(panes), paneOutput)
	}
	target := panes[1]

	old := startReloadWorker
	t.Cleanup(func() { startReloadWorker = old })
	var workerArgs []string
	startReloadWorker = func(argv []string, _ deps.StartOptions) error {
		workerArgs = append([]string(nil), argv...)
		return nil
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--config", configPath, "chat", "reload", "2",
		"--sock", socket, "--pane", target, "--1h", "on",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("explicit --sock/--pane on a multi-pane server was refused: rc=%d stderr=%q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "multiple panes") {
		t.Fatalf("scheduler still hit the multi-pane refusal despite an explicit --pane: stderr=%q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "reload scheduled") {
		t.Fatalf("schedule receipt = %q", stdout.String())
	}
	joined := strings.Join(workerArgs, "\x00")
	count := strings.Count(joined, "\x00--pane\x00")
	if count != 1 {
		t.Fatalf("worker argv carries %d --pane flags, want exactly 1 (the caller's own): %q", count, workerArgs)
	}
	if !strings.Contains(joined, "\x00--pane\x00"+target) {
		t.Fatalf("worker argv lost the caller's own --pane %s: %q", target, workerArgs)
	}
}

// reloadPromptFixture is a raw-tty pane the worker can actually /exit and
// respawn: it echoes every typed byte itself (like injectCLIUI in
// inject_cli_jail_test.go) so capture-pane sees a live "❯ …" composer line,
// and it exits 0 the moment a submitted line reads exactly "/exit" — the
// literal string reload.Run types and submits before ever touching Respawn.
const reloadPromptFixture = `import os, sys, tty
tty.setraw(0)
buf = bytearray()
sys.stdout.write("❯ ")
sys.stdout.flush()
while True:
    ch = os.read(0, 1)
    if not ch:
        break
    if ch == b"\x13":
        continue
    if ch in (b"\r", b"\n"):
        line = bytes(buf).decode("utf-8", "replace")
        buf.clear()
        if line == "/exit":
            sys.exit(0)
        sys.stdout.write("\r\n❯ ")
        sys.stdout.flush()
        continue
    buf.extend(ch)
    sys.stdout.buffer.write(ch)
    sys.stdout.flush()
`

// TestChatReloadWorkerFreshDropsSessionButKeepsTranscriptCWD is the
// regression for T1: `--new` must blank the resumed session id (so the
// respawned Claude never carries `--resume`) while the transcript-derived
// CWD still reaches the respawned pane untouched. It drives the worker
// through a REAL tmux pane end to end — cmd/pfm's worker owns no Tmux
// interface seam of its own (unlike internal/reload's fakeReloadTmux), so a
// live probe socket plus a substitute "claude" binary that records its own
// argv and working directory is this package's actual stand-in for a fake.
func TestChatReloadWorkerFreshDropsSessionButKeepsTranscriptCWD(t *testing.T) {
	root := jailTest(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}

	captured := filepath.Join(t.TempDir(), "captured.txt")
	fixtureClaude := filepath.Join(t.TempDir(), "claude-fixture.sh")
	script := "#!/bin/sh\n{\n  pwd\n  for a in \"$@\"; do printf 'ARG:%s\\n' \"$a\"; done\n} >> '" + captured + "'\nexit 0\n"
	if err := os.WriteFile(fixtureClaude, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	targetCWD := t.TempDir()

	configPath := writeConfigFixture(t, root, `{
  "version": 1,
  "accounts": [
    {"id": 1, "configDir": "`+filepath.Join(root, "account-1")+`", "claude": {"binary": "`+fixtureClaude+`"}}
  ]
}`)

	promptScript := filepath.Join(t.TempDir(), "prompt.py")
	if err := os.WriteFile(promptScript, []byte(reloadPromptFixture), 0o700); err != nil {
		t.Fatal(err)
	}
	socket := probeReloadSocket(t, "fresh")
	server := exec.Command(
		"tmux", "-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", "probe",
		"python3", promptScript,
	)
	server.Env = append(server.Environ(), "TMUX=")
	if output, err := server.CombinedOutput(); err != nil {
		t.Fatalf("start probe socket: %v: %s", err, output)
	}
	cleanupProbeReloadSocket(t, socket)

	paneOutput, err := exec.Command("tmux", "-S", socket, "list-panes", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatalf("read probe pane: %v", err)
	}
	pane := strings.TrimSpace(string(paneOutput))

	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(resolved.SIDDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The crumb's own filename supplies the session id (SessionFromCrumb
	// strips the extension off the transcript basename); the transcript
	// FILE supplies the cwd the worker must keep even under --new.
	transcript := filepath.Join(t.TempDir(), "44444444-4444-4444-8444-444444444444.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"cwd":"`+targetCWD+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	crumb := filepath.Join(resolved.SIDDir, filepath.Base(socket)+"."+pane)
	if err := os.WriteFile(crumb, []byte(transcript+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PFM_RELOAD_DELAY_MS", "0")
	t.Setenv("PFM_RELOAD_POLL_MS", "20")
	t.Setenv("PFM_RELOAD_EXIT_TRIES", "50")
	// The 20ms poll keeps the test quick, and it also shrinks the worker's
	// wait for the pane to show a composer (IdleTries × poll) to 2.4s at the
	// default 120 tries. On a loaded box python3 itself takes longer than
	// that to reach its first prompt, so the BUDGET, not the worker, would be
	// what ran out. 500 × 20ms = 10s of readiness wait, the same seconds the
	// default 120 × 1s gives a real chat.
	t.Setenv("PFM_RELOAD_IDLE_TRIES", "500")

	runtime, err := pfmconfig.LoadRuntime(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runChatReloadWorkerWithRuntime(
		[]string{"--sock", socket, "--new", "--account", "1"}, &stdout, &stderr, runtime, nil,
	)
	if code != 0 {
		t.Fatalf("fresh reload rc=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "rebooted FRESH as requested") {
		t.Fatalf("stdout=%q missing the fresh-reboot receipt", stdout.String())
	}

	// Respawn returns as soon as the tmux server has forked the replacement
	// process; the fixture claude finishing its own write is a separate,
	// microseconds-scale race — poll rather than assume the write already
	// landed by the time the worker call above returned.
	var content string
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, readErr := os.ReadFile(captured)
		if readErr == nil && len(data) > 0 {
			content = string(data)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"fixture claude never ran within the timeout: err=%v stdout=%q stderr=%q",
				readErr,
				stdout.String(),
				stderr.String(),
			)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Contains(content, "--resume") {
		t.Fatalf("fresh reload still resumed a session:\n%s", content)
	}
	if !strings.HasPrefix(content, targetCWD+"\n") {
		t.Fatalf("fresh reload lost the transcript's cwd: got %q, want it to start with %q", content, targetCWD+"\n")
	}
}

func probeReloadSocket(t *testing.T, suffix string) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "cc-probe-pfm-reload-"+strconv.Itoa(os.Getpid())+"-"+suffix)
}

func cleanupProbeReloadSocket(t *testing.T, socket string) {
	t.Helper()
	t.Cleanup(func() {
		command := exec.Command("tmux", "-S", socket, "kill-server")
		command.Env = append(command.Environ(), "TMUX=")
		if output, err := command.CombinedOutput(); err != nil {
			probe := exec.Command("tmux", "-S", socket, "list-panes")
			probe.Env = append(probe.Environ(), "TMUX=")
			if probe.Run() == nil {
				t.Errorf("stop probe server %s: %v: %s", socket, err, output)
			}
		}
		if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove stale probe socket %s: %v", socket, err)
		}
	})
}

func TestReloadTargetIdentityNeverFallsBackToTheCallerSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CODE_SESSION_ID", "11111111-1111-4111-8111-111111111111")
	resolved := paths.Values{
		SIDDir: filepath.Join(root, "sid"),
		Roots:  map[pfmengine.ID][]string{pfmengine.Claude: {filepath.Join(root, "claude")}},
	}
	_, _, err := resolveReloadSession(
		resolved,
		pfmconfig.Defaults(resolved.Home, resolved.Roots[pfmengine.Claude], resolved.FirstRoot(pfmengine.Codex)),
		"/tmp/tmux-1000/probe-pfm-reload-target",
		"%7",
		false,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "couldn't identify") {
		t.Fatalf("target without its own breadcrumb borrowed caller identity: %v", err)
	}
}

func TestExplicitCodexReloadUsesPaneBindingWithoutBreadcrumb(t *testing.T) {
	jailTest(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	machine := pfmconfig.Defaults(
		resolved.Home,
		resolved.Roots[pfmengine.Claude],
		resolved.FirstRoot(pfmengine.Codex),
	)
	machine.CodexAccounts = []pfmconfig.CodexAccount{{
		ID:   1,
		Home: resolved.FirstRoot(pfmengine.Codex),
	}}
	const (
		socket = "cx-probe-reload-bound"
		pane   = "%7"
		wantID = "22222222-2222-4222-8222-222222222222"
	)
	rollout := filepath.Join(
		resolved.FirstRoot(pfmengine.Codex),
		"sessions",
		"rollout-2026-08-24T00-00-00-"+wantID+".jsonl",
	)
	if err := os.MkdirAll(filepath.Dir(rollout), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rollout, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	manager, err := kill.New(database, fleet.KillDependencies(commandRuntime{
		Config: machine,
		Paths:  resolved,
	}))
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if _, _, err := manager.AdvanceCodexPane(context.Background(), socket, pane, wantID); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	gotID, gotTranscript, err := resolveReloadSession(
		resolved,
		machine,
		filepath.Join(resolved.TmuxDir, socket),
		pane,
		false,
		nil,
	)
	if err != nil || gotID != wantID || gotTranscript != rollout {
		t.Fatalf(
			"resolve bound Codex reload = (%q, %q, %v), want (%q, %q, nil)",
			gotID,
			gotTranscript,
			err,
			wantID,
			rollout,
		)
	}
}
