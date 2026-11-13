package mockengine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/statusline"
)

func TestClaudeStatuslineInputRendersThroughPfmsOwnDecoder(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.statusRefresh = 0.05
	fix.installHooks()
	// --model on the command line wins over the scenario's model, as on a
	// real launch; the display name is the family word the statusline shows.
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Model: "claude-opus-4-1", Steps: []Step{
		{
			Type:   StepTurn,
			Reply:  "rendered",
			Tokens: &Tokens{Input: 50, Output: 5, CacheRead: 1000, CacheCreation: 10, ContextWindow: 200000},
		},
	}})
	session := fix.startTUI("claude", claudeArgs("--name", "status seat", "--effort", "high"), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("paint me")
	session.waitFrame("the statusline output at the pane bottom", func(frame string) bool {
		return strings.Contains(frame, "rendered") && strings.Contains(frame, "SL-FIXTURE")
	})
	lines := fix.waitRecorded("statusline", 1)
	raw := lines[len(lines)-1]
	sidDir := filepath.Join(fix.root, "sl-sid")
	rendered, err := statusline.Render(context.Background(), []byte(raw), statusline.Runtime{
		Now:          func() time.Time { return time.Unix(1_786_838_400, 0) },
		Home:         fix.home,
		ConfigDir:    fix.configDir,
		CacheDir:     filepath.Join(fix.root, "sl-cache"),
		RateLimitDir: filepath.Join(fix.root, "sl-rates"),
		SIDDir:       sidDir,
		TmuxDir:      filepath.Join(fix.root, "sl-tmux"),
		ProcRoot:     filepath.Join(fix.root, "sl-proc"),
		UID:          1000,
		Engine:       pfmengine.Claude,
		Env:          map[string]string{},
		Command:      quietCommands{},
		Spawn:        func(statusline.RefreshKind) error { return nil },
	})
	if err != nil {
		t.Fatalf("pfm's statusline decoder refused the mock's input: %v\n%s", err, raw)
	}
	for _, want := range []string{"status seat", "repo", "Sonnet"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered statusline %q lacks %q (input %s)", rendered, want, raw)
		}
	}
	var input struct {
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
		ContextWindow  struct {
			UsedPercentage float64 `json:"used_percentage"`
			CurrentUsage   struct {
				InputTokens int64 `json:"input_tokens"`
				CacheRead   int64 `json:"cache_read_input_tokens"`
			} `json:"current_usage"`
		} `json:"context_window"`
		Effort struct {
			Level string `json:"level"`
		} `json:"effort"`
	}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	if input.SessionID != fixtureSession || input.TranscriptPath != fix.claudeTranscript(fixtureSession) ||
		input.ContextWindow.CurrentUsage.InputTokens != 50 || input.ContextWindow.CurrentUsage.CacheRead != 1000 ||
		input.ContextWindow.UsedPercentage <= 0 || input.Effort.Level != "high" {
		t.Fatalf("statusline input = %s", raw)
	}
	// /rename fires no hook: the statusline's next render (refreshInterval)
	// carries the new session_name, the one signal pfm's Herdr seat follows.
	session.typeLine("/rename status renamed")
	deadline := time.Now().Add(8 * time.Second)
	for {
		lines = fix.recorded("statusline")
		var renamed struct {
			SessionName string `json:"session_name"`
		}
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &renamed); err == nil &&
			renamed.SessionName == "status renamed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("statusline input after /rename = %s, want session_name %q", lines[len(lines)-1], "status renamed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Nothing happens in the pane from here: refreshInterval alone re-runs
	// the command, as Claude does, so a line pfm rendered late still lands.
	fix.waitRecorded("statusline", len(lines)+2)
}
