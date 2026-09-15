package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/inject"
	"hostops/pfm/internal/testjail"
)

const injectCLIUI = `import os, sys, tty
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
        if buf:
            text = bytes(buf).decode("utf-8", "replace")
            sys.stdout.write("\r\nUSER:" + text + "\r\n❯ ")
            sys.stdout.flush()
            buf.clear()
        continue
    buf.extend(ch)
    sys.stdout.buffer.write(ch)
    sys.stdout.flush()
`

func TestInjectFailurePrintsContradictionProofOnStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := writeInjectResult(inject.Result{
		Status:  "typed_unconfirmed",
		Code:    inject.CodeUndelivered,
		Message: "PROOF-CONTRADICTION: composer still holds the message",
		Proof:   "Working\n❯ stranded message",
	}, "probe-contradiction", &stdout, &stderr)
	if code != codeUndelivered || stdout.Len() != 0 {
		t.Fatalf("contradiction exit=%d stdout=%q", code, stdout.String())
	}
	for _, want := range []string{
		"PROOF-CONTRADICTION",
		"delivery proof (FAILED)",
		"❯ stranded message",
		"end delivery proof",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q lacks %q", stderr.String(), want)
		}
	}
}

func TestInjectReceiptPrintsResolverNoteOnStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := writeInjectResult(inject.Result{
		Status:         "delivered",
		Message:        "injected LIVE",
		Proof:          "USER: next\n❯ ",
		ResolutionNote: "resolved 🔖 label 'QA' → pane '%1'",
	}, "QA", &stdout, &stderr)
	if code != 0 || !strings.Contains(stderr.String(), "resolved 🔖 label") {
		t.Fatalf("resolver note exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestInjectAcceptsRetiredNoSigFlagWithWarning(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runHeadlessInject([]string{"--no-sig"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--no-sig is retired") ||
		strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("retired flag exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestChatInjectResolvesUnindexedLiveSessionAcrossProbeSockets(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary is not installed")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 binary is not installed")
	}
	tmuxRoot := filepath.Join(os.TempDir(), "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(tmuxRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(tmuxRoot, "probe-pfm-cli-inject-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(root)
	})
	home := filepath.Join(root, "home")
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	for _, directory := range []string{home, tmuxDir, filepath.Join(root, "sid"), filepath.Join(root, "claude"), filepath.Join(root, "codex"), filepath.Join(root, "proc")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", root)
	t.Setenv("TMPDIR", root)
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_DB", filepath.Join(root, "fleet.db"))
	t.Setenv("PFM_SHARED_DB", filepath.Join(root, "shared.db"))
	t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))
	t.Setenv("PFM_CLAUDE_ROOTS", filepath.Join(root, "claude"))
	t.Setenv("PFM_CODEX_ROOT", filepath.Join(root, "codex"))
	t.Setenv("PFM_TMUX_DIR", tmuxDir)
	t.Setenv("PFM_PROC_ROOT", filepath.Join(root, "proc"))
	t.Setenv("PFM_TMUX_CONF", "/dev/null")
	t.Setenv("CHAT_INJECT_POLL", "0.01")
	t.Setenv("CHAT_INJECT_ENTER_SETTLE", "0.05")
	t.Setenv("CHAT_INJECT_PROOF_SETTLE", "0.05")
	// Deliberately do not enable PFM_TEST_PROBE_SOCKETS: the fleet/store scan
	// must not know this seat. The inject resolver independently scans every
	// socket and is the behavior under test.

	script := filepath.Join(root, "ui.py")
	if err := os.WriteFile(script, []byte(injectCLIUI), 0o700); err != nil {
		t.Fatal(err)
	}
	socket := "probe-inj-direct-sock"
	session := "probe-inj-direct-session"
	command := exec.Command(
		"tmux",
		"-f",
		"/dev/null",
		"-L",
		socket,
		"new-session",
		"-d",
		"-s",
		session,
		"python3",
		script,
	)
	command.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root, "HOME="+home)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start probe tmux: %v: %s", err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-L", socket, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root, "HOME="+home)
		_ = kill.Run()
	})

	deadline := time.Now().Add(5 * time.Second)
	for {
		capture := exec.Command("tmux", "-L", socket, "capture-pane", "-t", session, "-p", "-J")
		capture.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root, "HOME="+home)
		output, captureErr := capture.Output()
		if captureErr == nil && strings.Contains(string(output), "❯") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe UI did not become ready: %v", captureErr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	statedTestSender(t)
	var stdout, stderr bytes.Buffer
	message := fmt.Sprintf("direct ladder probe %d", time.Now().UnixNano())
	code := run([]string{"chat", "inject", session, message}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("inject exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "injected LIVE") {
		t.Fatalf("receipt did not prove live delivery: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "delivery proof") ||
		!strings.Contains(stdout.String(), "USER:"+message) {
		t.Fatalf("receipt omitted the pane proof block: %q", stdout.String())
	}
	verify := exec.Command("tmux", "-L", socket, "capture-pane", "-t", session, "-p", "-J", "-S", "-")
	verify.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root, "HOME="+home)
	verified, err := verify.Output()
	if err != nil || !strings.Contains(string(verified), "USER:"+message) {
		t.Fatalf("live body missing after receipt: err=%v capture=%q", err, verified)
	}

	selectorScript := filepath.Join(root, "selector.py")
	if err := os.WriteFile(
		selectorScript,
		[]byte("import time\nprint('Question\\n❯ 1. Allow\\n  2. Deny', flush=True)\ntime.sleep(30)\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	selectorSocket := "probe-inj-selector-sock"
	selectorSession := "probe-inj-selector-session"
	selectorStart := exec.Command(
		"tmux",
		"-f",
		"/dev/null",
		"-L",
		selectorSocket,
		"new-session",
		"-d",
		"-s",
		selectorSession,
		"python3",
		selectorScript,
	)
	selectorStart.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root, "HOME="+home)
	if output, err := selectorStart.CombinedOutput(); err != nil {
		t.Fatalf("start selector probe: %v: %s", err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-L", selectorSocket, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root, "HOME="+home)
		_ = kill.Run()
	})
	selectorDeadline := time.Now().Add(5 * time.Second)
	for {
		capture := exec.Command("tmux", "-L", selectorSocket, "capture-pane", "-t", selectorSession, "-p", "-J")
		capture.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root, "HOME="+home)
		output, captureErr := capture.Output()
		if captureErr == nil && strings.Contains(string(output), "❯ 1. Allow") {
			break
		}
		if time.Now().After(selectorDeadline) {
			t.Fatalf("selector probe did not become ready: err=%v capture=%q", captureErr, output)
		}
		time.Sleep(20 * time.Millisecond)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"chat", "inject", selectorSession, "must not type"}, &stdout, &stderr)
	if code != codeUndelivered || stdout.Len() != 0 || !strings.Contains(stderr.String(), "OPEN selector") {
		t.Fatalf("selector exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	fileSocket := "probe-cx-inj-file-sock"
	fileSession := "probe-inj-file-session"
	fileStart := exec.Command(
		"tmux",
		"-f",
		"/dev/null",
		"-L",
		fileSocket,
		"new-session",
		"-d",
		"-s",
		fileSession,
		"python3",
		script,
	)
	fileStart.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root, "HOME="+home)
	if output, err := fileStart.CombinedOutput(); err != nil {
		t.Fatalf("start file-backed probe: %v: %s", err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-L", fileSocket, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root, "HOME="+home)
		_ = kill.Run()
	})
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "1")
	// Five kilobytes now travels whole through bracketed paste instead of
	// being replaced by an auto-file pointer (Task C): it must reach the
	// pane byte-exact, and it must never be typed as a truncated inline
	// argument or leave the raw "--file PATH" flag text sitting in the
	// composer as if it were the body.
	longBody := "file-start " + strings.Repeat("0123456789abcdef", 320) + " file-end"
	messagePath := filepath.Join(root, "long-message.txt")
	if err := os.WriteFile(messagePath, []byte(longBody), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"chat", "inject", "--file", messagePath, fileSession}, &stdout, &stderr)
	if code != 0 || strings.Contains(stdout.String(), "AUTO-FILE") ||
		!strings.Contains(stdout.String(), "FILE-BACKED") {
		t.Fatalf("file-backed exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	// The embedded receipt proof is bounded to the pane's tail lines, so for
	// a multi-KB body it shows the END of the body, not "file-start" (which
	// scrolled off) — the full-scrollback capture below (fileOutput) is what
	// proves the complete body, not this bounded excerpt.
	if !strings.Contains(stdout.String(), "delivery proof") ||
		!strings.Contains(stdout.String(), "file-end") {
		t.Fatalf("file-backed receipt omitted pane proof: %q", stdout.String())
	}
	// A delivery the pane already proves (tail match, below) must not ALSO
	// spill a canonical body file — the auto-file store is a rescue for an
	// unproven paste, not the default any more.
	canonical, err := filepath.Glob(filepath.Join(
		home, ".local", "state", "pfm", "inject-bodies", "*.md",
	))
	if err != nil || len(canonical) != 0 {
		t.Fatalf("a proven paste delivery spilled a canonical body file: %q err=%v", canonical, err)
	}
	fileCapture := exec.Command("tmux", "-L", fileSocket, "capture-pane", "-t", fileSession, "-p", "-J", "-S", "-")
	fileCapture.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root, "HOME="+home)
	fileOutput, err := fileCapture.Output()
	if err != nil || !strings.Contains(string(fileOutput), longBody) ||
		strings.Contains(string(fileOutput), "--file "+messagePath) {
		t.Fatalf("file-backed body missing, truncated, or leaked the raw flag text: err=%v capture=%q", err, fileOutput)
	}

	// The old shell accepted the compatibility spelling with the target first.
	// It must still consume --file as a flag; the raw flag text is not a body.
	compatBody := "compat-start " + strings.Repeat("zyxw", 1300) + " compat-end"
	compatPath := filepath.Join(root, "compat-long-message.txt")
	if err := os.WriteFile(compatPath, []byte(compatBody), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"chat", "inject", fileSession, "--file", compatPath}, &stdout, &stderr)
	if code != 0 || strings.Contains(stdout.String(), "AUTO-FILE") ||
		!strings.Contains(stdout.String(), "FILE-BACKED") {
		t.Fatalf("target-followed --file exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), compatPath) ||
		strings.Contains(stdout.String(), "injected LIVE into") {
		t.Fatalf("target-followed receipt lost file-backed provenance: %q", stdout.String())
	}
	compatCapture := exec.Command("tmux", "-L", fileSocket, "capture-pane", "-t", fileSession, "-p", "-J", "-S", "-")
	compatCapture.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root, "HOME="+home)
	compatOutput, err := compatCapture.Output()
	if err != nil || !strings.Contains(string(compatOutput), compatBody) ||
		strings.Contains(string(compatOutput), "--file "+compatPath) {
		t.Fatalf(
			"target-followed --file delivered the wrong body or leaked the raw flag text: err=%v capture=%q",
			err,
			compatOutput,
		)
	}
	// Both file-backed deliveries in this test proved themselves on the pane
	// (tail match); neither should have needed the auto-file rescue.
	compatCanonical, err := filepath.Glob(filepath.Join(
		home, ".local", "state", "pfm", "inject-bodies", "*.md",
	))
	if err != nil || len(compatCanonical) != 0 {
		t.Fatalf("a proven paste delivery spilled a canonical body file: %q err=%v", compatCanonical, err)
	}
}

func TestChatInjectResumeLadderPathSessionAndExcerpt(t *testing.T) {
	const id = "11111111-2222-4333-8444-555555555555"
	for _, testCase := range []struct {
		name   string
		target func(string) string
	}{
		{name: "transcript path", target: func(path string) string { return path }},
		{name: "session id", target: func(string) string { return id }},
		{name: "distinctive excerpt", target: func(string) string {
			return "distinctive transcript sentence for the resume ladder"
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := testjail.ShortRoot(t)
			home := filepath.Join(root, "home")
			project := filepath.Join(root, "claude", "project")
			for _, directory := range []string{home, project, filepath.Join(root, "tmux"), filepath.Join(root, "codex"), filepath.Join(root, "proc")} {
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			transcript := filepath.Join(project, id+".jsonl")
			line := fmt.Sprintf(
				`{"type":"user","uuid":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","sessionId":%q,"cwd":"/jail/project","version":"1","gitBranch":"","timestamp":"2026-08-16T00:00:00.000Z","message":{"role":"user","content":"distinctive transcript sentence for the resume ladder"}}`+"\n",
				id,
			)
			if err := os.WriteFile(transcript, []byte(line), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("TMUX", "")
			t.Setenv("PFM_HOME", home)
			t.Setenv("PFM_DB", filepath.Join(root, "fleet.db"))
			t.Setenv("PFM_SHARED_DB", filepath.Join(root, "shared.db"))
			t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))
			t.Setenv("PFM_CLAUDE_ROOTS", filepath.Join(root, "claude"))
			t.Setenv("PFM_CODEX_ROOT", filepath.Join(root, "codex"))
			t.Setenv("PFM_TMUX_DIR", filepath.Join(root, "tmux"))
			t.Setenv("PFM_PROC_ROOT", filepath.Join(root, "proc"))
			statedTestSender(t)
			var stdout, stderr bytes.Buffer
			code := run(
				[]string{"chat", "inject", testCase.target(transcript), "resume ladder message"},
				&stdout,
				&stderr,
			)
			if code != 0 {
				t.Fatalf("inject exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), "RESUME") {
				t.Fatalf("receipt did not name RESUME: %q", stdout.String())
			}
			raw, err := os.ReadFile(transcript)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), "resume ladder message") || strings.Count(string(raw), "\n") != 2 {
				t.Fatalf("transcript append = %q", raw)
			}
			backups, err := filepath.Glob(filepath.Join(home, ".claude-sessions", ".chat-inject-backups", "*.jsonl"))
			if err != nil || len(backups) != 1 {
				t.Fatalf("backups=%v err=%v", backups, err)
			}
		})
	}
}

func TestChatInjectResumeRefusesAProcessHeldSessionAsDead(t *testing.T) {
	const id = "99999999-2222-4333-8444-555555555555"
	root := testjail.ShortRoot(t)
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "claude", "project")
	process := filepath.Join(root, "proc", "4242")
	for _, directory := range []string{home, project, process, filepath.Join(root, "tmux"), filepath.Join(root, "codex")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	transcript := filepath.Join(project, id+".jsonl")
	line := fmt.Sprintf(
		`{"type":"user","uuid":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","sessionId":%q,"message":{"role":"user","content":"held"}}`+"\n",
		id,
	)
	if err := os.WriteFile(transcript, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(process, "environ"),
		[]byte("CLAUDE_CODE_SESSION_ID="+id+"\x00"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("TMUX", "")
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_DB", filepath.Join(root, "fleet.db"))
	t.Setenv("PFM_SHARED_DB", filepath.Join(root, "shared.db"))
	t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))
	t.Setenv("PFM_CLAUDE_ROOTS", filepath.Join(root, "claude"))
	t.Setenv("PFM_CODEX_ROOT", filepath.Join(root, "codex"))
	t.Setenv("PFM_TMUX_DIR", filepath.Join(root, "tmux"))
	t.Setenv("PFM_PROC_ROOT", filepath.Join(root, "proc"))
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "inject", id, "must not append"}, &stdout, &stderr)
	if code != codeDeadChat || stdout.Len() != 0 || !strings.Contains(stderr.String(), "LIVE session") {
		t.Fatalf("dead guard exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	raw, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != line {
		t.Fatalf("dead guard mutated transcript: %q", raw)
	}
}

func TestChatInjectResumeRefusesDaemonRegistrySession(t *testing.T) {
	const id = "77777777-2222-4333-8444-555555555555"
	root := testjail.ShortRoot(t)
	home := filepath.Join(root, "home")
	accountProjects := filepath.Join(home, ".cc", "1", "projects")
	project := filepath.Join(accountProjects, "fixture")
	bin := filepath.Join(root, "bin")
	for _, directory := range []string{
		project,
		bin,
		filepath.Join(root, "sid"),
		filepath.Join(root, "tmux"),
		filepath.Join(root, "codex"),
		filepath.Join(root, "proc"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	transcript := filepath.Join(project, id+".jsonl")
	line := fmt.Sprintf(
		`{"type":"user","uuid":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","sessionId":%q,"message":{"role":"user","content":"held"}}`+"\n",
		id,
	)
	if err := os.WriteFile(transcript, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(bin, "claude")
	registry := fmt.Sprintf(`[{"sessionId":%q,"id":"77777777","status":"busy"}]`, id)
	if err := os.WriteFile(claude, []byte("#!/bin/sh\nprintf '%s\\n' '"+registry+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("TMUX", "")
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_DB", filepath.Join(root, "fleet.db"))
	t.Setenv("PFM_SHARED_DB", filepath.Join(root, "shared.db"))
	t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))
	t.Setenv("PFM_CLAUDE_ROOTS", accountProjects)
	t.Setenv("PFM_CODEX_ROOT", filepath.Join(root, "codex"))
	t.Setenv("PFM_TMUX_DIR", filepath.Join(root, "tmux"))
	t.Setenv("PFM_PROC_ROOT", filepath.Join(root, "proc"))
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "inject", id, "must not append"}, &stdout, &stderr)
	if code != codeDeadChat || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "LIVE background agent") {
		t.Fatalf("daemon guard exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	raw, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != line {
		t.Fatalf("daemon guard mutated transcript: %q", raw)
	}
}

func TestChatInjectResumeRefusesLiveSocketCrumbSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary is not installed")
	}
	const id = "66666666-2222-4333-8444-555555555555"
	const socket = "cc-1800000000-22-3"
	base := filepath.Join(os.TempDir(), "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "probe-resume-crumb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	home := filepath.Join(root, "home")
	accountProjects := filepath.Join(home, ".cc", "1", "projects")
	project := filepath.Join(accountProjects, "fixture")
	sidDir := filepath.Join(root, "sid")
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	for _, directory := range []string{
		project,
		sidDir,
		tmuxDir,
		filepath.Join(root, "codex"),
		filepath.Join(root, "proc"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	transcript := filepath.Join(project, id+".jsonl")
	line := fmt.Sprintf(
		`{"type":"user","uuid":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","sessionId":%q,"message":{"role":"user","content":"held"}}`+"\n",
		id,
	)
	if err := os.WriteFile(transcript, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sidDir, socket), []byte(transcript+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	start := exec.Command(
		"tmux",
		"-f",
		"/dev/null",
		"-L",
		socket,
		"new-session",
		"-d",
		"-s",
		"probe-resume",
		"sleep",
		"30",
	)
	start.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start crumb probe: %v: %s", err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-L", socket, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
		_ = kill.Run()
	})
	t.Setenv("HOME", home)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", root)
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_DB", filepath.Join(root, "fleet.db"))
	t.Setenv("PFM_SHARED_DB", filepath.Join(root, "shared.db"))
	t.Setenv("PFM_SID_DIR", sidDir)
	t.Setenv("PFM_CLAUDE_ROOTS", accountProjects)
	t.Setenv("PFM_CODEX_ROOT", filepath.Join(root, "codex"))
	t.Setenv("PFM_TMUX_DIR", tmuxDir)
	t.Setenv("PFM_PROC_ROOT", filepath.Join(root, "proc"))
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "inject", id, "must not append"}, &stdout, &stderr)
	if code != codeDeadChat || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "LIVE session") {
		t.Fatalf("crumb guard exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	raw, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != line {
		t.Fatalf("crumb guard mutated transcript: %q", raw)
	}
}
