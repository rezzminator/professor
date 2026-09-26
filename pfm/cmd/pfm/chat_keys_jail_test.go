package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestChatKeysRefusesATokenTmuxWouldType guards the silent failure the whole
// verb exists to avoid: tmux types an unrecognised key name as text, so a
// caller who means "press escape" and writes `Esc` puts three letters into the
// composer and is told it worked.
func TestChatKeysRefusesATokenTmuxWouldType(t *testing.T) {
	for _, token := range []string{"Esc", "Return", "ctrl-c", ""} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"chat", "keys", "any-target", token}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("%q exit=%d, want 2 (stdout=%q stderr=%q)",
				token, code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "is not a tmux key") ||
			!strings.Contains(stderr.String(), "--literal") {
			t.Fatalf("%q refusal = %q, want the reason and the way to mean it",
				token, stderr.String())
		}
	}
}

// TestChatKeysAcceptsTheKeysTmuxPresses is the other side of that gate: every
// name a caller reaches for must survive validation, or the guard becomes the
// bug.
func TestChatKeysAcceptsTheKeysTmuxPresses(t *testing.T) {
	for _, key := range []string{
		"Enter", "Escape", "Tab", "BTab", "BSpace", "Space", "Up", "Down",
		"Left", "Right", "Home", "End", "PageUp", "PageDown", "F1", "F12",
		"C-c", "C-o", "M-x", "S-Tab", "C-M-a",
	} {
		if !validKey(key) {
			t.Fatalf("%q rejected, want it accepted", key)
		}
	}
	for _, key := range []string{"F0", "F13", "Esc", "Ctrl-C", "C-", ""} {
		if validKey(key) {
			t.Fatalf("%q accepted, want it rejected", key)
		}
	}
}

// TestChatKeysDrivesALiveChat proves the verb reaches the engine, not just
// tmux: text typed with --literal and submitted with Enter has to come back
// out of the chat's own transcript.
func TestChatKeysDrivesALiveChat(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"chat", "new",
		"--name", "keys worker",
		"--cwd", jail.root + "/work",
		"first orders",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("run exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	socket := socketFromRun(t, stdout.String())

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{
		"chat", "keys", "--delay", "10ms", "--literal", socket, "typed by keys",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("literal exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{
		"chat", "keys", "--delay", "10ms", socket, "Enter",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("Enter exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 key(s)") {
		t.Fatalf("receipt = %q, want the count of keys pressed", stdout.String())
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		var transcript, transcriptErrors bytes.Buffer
		run([]string{"chat", "read", socket, "--tail", "10", "--condensed"},
			&transcript, &transcriptErrors)
		if strings.Contains(transcript.String(), "typed by keys") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the keys never reached the engine: %q", transcript.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestChatKeysRefusesADeadChat keeps the verb from reporting a press into a
// pane that is not there.
func TestChatKeysRefusesADeadChat(t *testing.T) {
	// Hermetic by law: without a jail override this verb would open the
	// OPERATOR'S live database — which a newer mirror may already have
	// migrated past this build's schema, turning an expected codeUnknownChat
	// into a schema refusal.
	t.Setenv("PFM_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "keys", "no-such-chat-here", "Enter"}, &stdout, &stderr)
	if code != codeUnknownChat {
		t.Fatalf("exit=%d, want %d (stderr=%q)", code, codeUnknownChat, stderr.String())
	}
}

// TestRunRescuesAPromptStrandedByAnOverlay is the failure that stranded a
// launch for twenty minutes: the prompt was typed, the startup overlay ate the
// Enter, and the chat sat idle looking alive. The launch has to press the keys
// a human would and prove the result against the engine's transcript.
func TestRunRescuesAPromptStrandedByAnOverlay(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)
	t.Setenv("CC_STUB_OVERLAY", "1")
	restoreGrace, restoreWindow := launchGrace, launchProofWindow
	launchGrace, launchProofWindow = 50*time.Millisecond, 250*time.Millisecond
	t.Cleanup(func() { launchGrace, launchProofWindow = restoreGrace, restoreWindow })

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new",
		"--name", "stranded worker",
		"--cwd", jail.root + "/work",
		"a prompt the overlay swallowed",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d, want the rescue to succeed (stdout=%q stderr=%q)",
			code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unsent in the composer") ||
		!strings.Contains(stderr.String(), "the model recorded it") {
		t.Fatalf("rescue report = %q, want it to say what it did", stderr.String())
	}
}
