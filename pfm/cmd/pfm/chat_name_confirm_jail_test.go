//go:build jail

package main

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestApplyChatNameReadsAClaudeRenameBack pins the live-rename door of a
// Claude chat: `pfm chat name` (and MCP name, which runs it) reports a
// /rename that never reached the transcript instead of claiming it. Fixture:
// TestApplyChatNameRecordsAChatStateTransition's real tmux probe socket.
func TestApplyChatNameReadsAClaudeRenameBack(t *testing.T) {
	testjail.TmuxServer(t)
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
