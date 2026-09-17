package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/headless"
)

// TestSupersededAnswerIsNotAClean0: when a second message reached the chat
// mid-wait, the printed answer may be theirs, not ours. A script reading `$?`
// must learn that from the exit code — stderr alone is a warning nobody
// scripting `$(pfm chat ask …)` ever sees.
func TestSupersededAnswerIsNotAClean0(t *testing.T) {
	var stdout, stderr bytes.Buffer
	turn := headless.Turn{
		Name:       "worker",
		Delivered:  true,
		Answer:     "one answer for both",
		Superseded: true,
		State:      headless.StateIdle,
	}
	code := reportTurn(turn, nil, "ask", "worker", 30*time.Second, false, &stdout, &stderr)
	if code != codeAwaitSuperseded {
		t.Fatalf("exit = %d, want %d (codeAwaitSuperseded); stderr=%q", code, codeAwaitSuperseded, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "one answer for both" {
		t.Fatalf("stdout = %q, want the newest answer alone", stdout.String())
	}
	if !strings.Contains(stderr.String(), "may be theirs") {
		t.Fatalf("stderr = %q, want the supersede notice", stderr.String())
	}
	// A clean, unshared answer stays 0.
	turn.Superseded = false
	if code := reportTurn(turn, nil, "ask", "worker", 30*time.Second, false, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d for an unshared answer, want 0", code)
	}
}
