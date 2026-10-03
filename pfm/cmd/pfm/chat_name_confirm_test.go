package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
)

const (
	titledFixLogin = `{"type":"custom-title","customTitle":"Fix login","sessionId":"s"}`
	titledOther    = `{"type":"custom-title","customTitle":"Other","sessionId":"s"}`
	userTurn       = `{"type":"user","message":{"role":"user","content":"hi"},"sessionId":"s"}`
)

func shortClaudeRenameConfirm(t *testing.T) {
	t.Helper()
	tries, poll := claudeRenameConfirmTries, claudeRenameConfirmPoll
	claudeRenameConfirmTries, claudeRenameConfirmPoll = 2, time.Millisecond
	t.Cleanup(func() { claudeRenameConfirmTries, claudeRenameConfirmPoll = tries, poll })
}

func writeConfirmTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUnconfirmedClaudeName(t *testing.T) {
	shortClaudeRenameConfirm(t)
	for _, scenario := range []struct {
		name string
		path string
		want string
	}{
		{"the transcript wears the name", writeConfirmTranscript(t, userTurn, titledFixLogin), ""},
		{"a later title replaced it", writeConfirmTranscript(t, titledFixLogin, titledOther), "NOT confirmed"},
		{"no title record at all", writeConfirmTranscript(t, userTurn), "NOT confirmed"},
		{"the transcript cannot be read", filepath.Join(t.TempDir(), "gone.jsonl"), "could not verify"},
		{"no transcript is known", "", "could not verify"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			got := unconfirmedClaudeName(context.Background(), headless.Chat{Path: scenario.path}, "Fix login")
			if scenario.want == "" && got != "" || scenario.want != "" && !strings.Contains(got, scenario.want) {
				t.Fatalf("unconfirmedClaudeName = %q, want %q", got, scenario.want)
			}
		})
	}
}

// TestApplyChatNameReadsAClaudeRenameBack pins the live-rename door of a
// Claude chat: `pfm chat name` (and MCP chat_name, which runs it) reports a
// /rename that never reached the transcript instead of claiming it. Fixture:
// TestApplyChatNameRecordsAChatStateTransition's real tmux probe socket.
func TestApplyChatNameReadsAClaudeRenameBack(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	shortClaudeRenameConfirm(t)
	socket := filepath.Join(t.TempDir(), "probe-nameread.sock")
	session := "probe-nameread"
	start := exec.Command(
		"tmux", "-S", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", session, "-n", "before", "sleep", "120",
	)
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start probe server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socket, "kill-server").Run()
	})
	var delivered []string
	deliver := func(_ context.Context, _ headless.Chat, name string) (int, string, error) {
		delivered = append(delivered, name)
		return 0, "", nil
	}
	for _, scenario := range []struct {
		name       string
		transcript string
		warns      bool
	}{
		{"a landed rename is quiet", writeConfirmTranscript(t, userTurn, titledFixLogin), false},
		{"an unlanded rename warns", writeConfirmTranscript(t, userTurn), true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := applyChatName(context.Background(), headless.Chat{
				ID: "probe-nameread-id", Name: "before", Engine: pfmengine.Claude,
				Path: scenario.transcript, Socket: socket, Session: session, Live: true,
			}, "Fix login", deliver, &stderr)
			if code != 0 {
				t.Fatalf("applyChatName code=%d stderr=%q", code, stderr.String())
			}
			if warned := strings.Contains(stderr.String(), "NOT confirmed"); warned != scenario.warns {
				t.Fatalf("unconfirmed warning=%t, want %t; stderr=%q", warned, scenario.warns, stderr.String())
			}
		})
	}
	if len(delivered) != 2 || delivered[0] != "Fix login" {
		t.Fatalf("delivered names = %q", delivered)
	}
}
