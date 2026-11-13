package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
