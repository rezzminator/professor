package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// socketFromRun reads the socket out of a run report line, which is the handle
// every later verb addresses the chat by.
func socketFromRun(t *testing.T, report string) string {
	t.Helper()
	for _, line := range strings.Split(report, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 3 && (strings.HasPrefix(fields[2], "cc-") ||
			strings.HasPrefix(fields[2], "cx-")) {
			return fields[2]
		}
	}
	t.Fatalf("no socket in run report: %q", report)
	return ""
}

// TestRunAwaitsTheAnswerItAskedFor is the launch half of two-way: one command
// starts a chat, gives it work, and comes back with the reply — with the
// answer alone on stdout, so a caller can capture it.
func TestRunAwaitsTheAnswerItAskedFor(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new",
		"--name", "await worker",
		"--cwd", jail.root + "/work",
		"--await", "--timeout", "60", "--settle", "1",
		"audit the firewall",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "ack: audit the firewall" {
		t.Fatalf("stdout = %q, want the answer alone", got)
	}
	if !strings.Contains(stderr.String(), "attach: tmux -L cc-") {
		t.Fatalf("the launch summary did not move to stderr: %q", stderr.String())
	}
}

// TestAskHoldsATwoWayConversation is the other half: a chat that is already
// running answers one question, then another, and each answer belongs to its
// own question.
func TestAskHoldsATwoWayConversation(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	statedTestSender(t)
	defer jail.killSockets(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"chat", "new",
		"--name", "talker",
		"--cwd", jail.root + "/work",
		"open the shop",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("run exit=%d stderr=%q", code, stderr.String())
	}
	socket := socketFromRun(t, stdout.String())

	for _, question := range []string{"first question", "second question"} {
		var out, errors bytes.Buffer
		code := run([]string{
			"chat", "ask", "--timeout", "60", "--settle", "0",
			socket, question,
		}, &out, &errors)
		if code != 0 {
			t.Fatalf("ask %q exit=%d out=%q err=%q", question, code, out.String(), errors.String())
		}
		answer := strings.TrimSpace(out.String())
		if !strings.HasPrefix(answer, "ack: "+question) {
			t.Fatalf("answer to %q = %q, want the reply to THAT question", question, answer)
		}
	}
}

// TestAskReportsATimeoutWithoutLosingDelivery: a chat that hears the question
// and says nothing is a different outcome from a chat that never heard it, and
// the exit code has to tell them apart.
func TestAskReportsATimeoutWithoutLosingDelivery(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	statedTestSender(t)
	defer jail.killSockets(t)
	t.Setenv("STUB_MUTE", "1")

	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"chat", "new",
		"--name", "mute worker",
		"--cwd", jail.root + "/work",
		"say nothing",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("run exit=%d stderr=%q", code, stderr.String())
	}
	socket := socketFromRun(t, stdout.String())

	var out, errorsOut bytes.Buffer
	code := run([]string{
		"chat", "ask", "--timeout", "1", "--settle", "0",
		socket, "answer me",
	}, &out, &errorsOut)
	if code != codeAwaitTimeout {
		t.Fatalf("ask exit=%d, want %d (out=%q err=%q)",
			code, codeAwaitTimeout, out.String(), errorsOut.String())
	}
	if !strings.Contains(errorsOut.String(), "the message was delivered") {
		t.Fatalf("timeout report = %q", errorsOut.String())
	}
	// The question itself must still have landed in the chat's record.
	var transcript, transcriptErrors bytes.Buffer
	if code := run([]string{
		"chat", "read", socket, "--tail", "5", "--condensed",
	}, &transcript, &transcriptErrors); code != 0 {
		t.Fatalf("transcript exit=%d err=%q", code, transcriptErrors.String())
	}
	if !strings.Contains(transcript.String(), "answer me") {
		t.Fatalf("transcript = %q, want the delivered question", transcript.String())
	}
}

// TestRunRefusesToCallAnUnheardPromptDelivered is the whole point of the
// verification: an engine that never records the prompt must not be reported
// as a working chat that got its orders.
func TestRunRefusesToCallAnUnheardPromptDelivered(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)
	t.Setenv("CC_STUB_DEAF", "1")
	restoreGrace, restoreWindow := launchGrace, launchProofWindow
	restoreRescue := launchRescueWindow
	launchGrace, launchProofWindow = 50*time.Millisecond, 250*time.Millisecond
	launchRescueWindow = 250 * time.Millisecond
	t.Cleanup(func() {
		launchGrace, launchProofWindow = restoreGrace, restoreWindow
		launchRescueWindow = restoreRescue
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new",
		"--name", "deaf worker",
		"--cwd", jail.root + "/work",
		"a prompt that will be eaten",
	}, &stdout, &stderr)
	if code != codeUndelivered {
		t.Fatalf("run exit=%d, want %d (stdout=%q stderr=%q)",
			code, codeUndelivered, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "never recorded the prompt") ||
		!strings.Contains(stderr.String(), "attach it and look") {
		t.Fatalf("refusal = %q, want it to name the failure and how to look", stderr.String())
	}
	// The retry is reported whether or not it worked: a rescue that silently
	// failed would read as a launch that was never retried.
	if !strings.Contains(stderr.String(), "retry did not") {
		t.Fatalf("refusal = %q, want it to say the retry was tried", stderr.String())
	}
	// The chat itself is still up: an undelivered prompt is not a reason to
	// kill a seat the user may want to drive by hand.
	if !strings.Contains(stdout.String(), "\tnamed\t") {
		t.Fatalf("run report = %q, want the live chat still reported", stdout.String())
	}
}
