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
		{"a later title replaced it", writeConfirmTranscript(t, titledFixLogin, titledOther), "the Claude rename was typed but NOT confirmed — no custom-title record naming it reached {path} within 1ms (a chat mid-turn may still apply it when its turn ends); check it with pfm ls"},
		{"no title record at all", writeConfirmTranscript(t, userTurn), "the Claude rename was typed but NOT confirmed — no custom-title record naming it reached {path} within 1ms (a chat mid-turn may still apply it when its turn ends); check it with pfm ls"},
		{"the transcript cannot be read", filepath.Join(t.TempDir(), "gone.jsonl"), "could not verify the Claude rename — read transcript \"{path}\" for its title: open \"{path}\": open {path}: no such file or directory; the chat may be named, check it with pfm ls"},
		{"no transcript is known", "", "could not verify the Claude rename — the chat's transcript is not known; the chat may be named, check it with pfm ls"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			got := unconfirmedClaudeName(context.Background(), headless.Chat{Path: scenario.path}, "Fix login")
			if scenario.path != "" {
				got = strings.ReplaceAll(got, scenario.path, "{path}")
			}
			if got != scenario.want {
				t.Fatalf("unconfirmedClaudeName = %q, want %q", got, scenario.want)
			}
		})
	}
}
