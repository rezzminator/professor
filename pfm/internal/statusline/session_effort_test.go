package statusline

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const effortSession = "sess-effort"

func recordMainLine(t *testing.T, sidDir, model, level string) {
	t.Helper()
	var warn bytes.Buffer
	RecordSession([]byte(`{"session_id":"`+effortSession+`","model":{"id":`+jsonText(model)+`},`+
		`"effort":{"level":`+jsonText(level)+`},"context_window":{"used_percentage":12}}`), sidDir, &warn)
	if warn.Len() != 0 {
		t.Fatalf("RecordSession warned: %q", warn.String())
	}
}

// renderEffortRow renders one non-agent task row for effortSession and
// returns its body and whatever went to warn.
func renderEffortRow(t *testing.T, sidDir, task string) (content, warned string) {
	t.Helper()
	var warn bytes.Buffer
	payload := `{"session_id":"` + effortSession + `","tasks":[` + task + `]}`
	got, err := RenderSubagents([]byte(payload), subagentNow, sidDir, &warn)
	if err != nil {
		t.Fatalf("RenderSubagents: %v", err)
	}
	var row subagentRow
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &row); err != nil {
		t.Fatalf("row %q: %v", got, err)
	}
	return row.Content, warn.String()
}

const (
	taskWithoutEffort = `{"id":"a","status":"running","model":"claude-opus-5-5","tokenCount":10,"contextWindowSize":100}`
	taskWithEffort    = `{"id":"a","status":"running","model":"claude-opus-5-5","effort":"low","tokenCount":10,"contextWindowSize":100}`
)

var (
	opusAlone     = cModel + "opus" + reset
	opusInherited = opusAlone + cMuted + "·" + reset + cEffort + "● high" + reset
)

func TestRecordSessionRecordsTheMainLineEffort(t *testing.T) {
	sidDir := t.TempDir()
	recordMainLine(t, sidDir, "claude-opus-5-5[1m]", "high")
	got, err := inheritedEffort(sidDir, effortSession)
	if err != nil || got != (sessionEffortRecord{Level: "high", Model: "claude-opus-5-5[1m]"}) {
		t.Fatalf("recorded effort = %+v err=%v, want high for claude-opus-5-5[1m]", got, err)
	}
}

func TestSubagentRowPayloadEffortWinsOverTheSessionRecord(t *testing.T) {
	sidDir := t.TempDir()
	recordMainLine(t, sidDir, "claude-opus-5-5[1m]", "high")
	content, _ := renderEffortRow(t, sidDir, taskWithEffort)
	if !strings.Contains(content, opusAlone+cMuted+"·"+reset+cEffort+"○ low"+reset) ||
		strings.Contains(content, "high") {
		t.Fatalf("row = %q, want the payload's own effort low", content)
	}
}

// A sub-agent with no effort of its own runs at the session's effort
// (Claude Code resolves it from the parent's sessionEffort), so the row shows
// it as plainly as a payload effort when the models match.
func TestSubagentRowWithoutEffortShowsTheSessionEffort(t *testing.T) {
	sidDir := t.TempDir()
	recordMainLine(t, sidDir, "claude-opus-5-5[1m]", "high")
	content, warned := renderEffortRow(t, sidDir, taskWithoutEffort)
	if !strings.Contains(content, opusInherited) || warned != "" {
		t.Fatalf("row = %q warn=%q, want inherited %q", content, warned, opusInherited)
	}
}

// On another model Claude Code re-resolves the level for that model (a
// per-model settings table, max and xhigh clamped where unsupported), so the
// main line's level is a best guess and renders muted.
func TestSubagentRowOnAnotherModelMutesTheSessionEffort(t *testing.T) {
	sidDir := t.TempDir()
	recordMainLine(t, sidDir, "claude-sonnet-5[1m]", "high")
	content, _ := renderEffortRow(t, sidDir, taskWithoutEffort)
	if want := opusAlone + cMuted + "·" + reset + cMuted + "● high" + reset; !strings.Contains(content, want) {
		t.Fatalf("row = %q, want muted guess %q", content, want)
	}
}

func TestSubagentRowWithoutEffortOrRecordShowsTheModelAlone(t *testing.T) {
	content, warned := renderEffortRow(t, t.TempDir(), taskWithoutEffort)
	if !strings.Contains(content, opusAlone) || strings.Contains(content, opusAlone+cMuted+"·") || warned != "" {
		t.Fatalf("row = %q warn=%q, want the model alone and no warning", content, warned)
	}
}

func TestSubagentRowWithAnUnreadableRecordShowsTheModelAloneAndWarns(t *testing.T) {
	sidDir := t.TempDir()
	recordMainLine(t, sidDir, "claude-opus-5-5[1m]", "high")
	if err := os.WriteFile(sessionEffortPath(sidDir, effortSession), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	content, warned := renderEffortRow(t, sidDir, taskWithoutEffort)
	if !strings.Contains(content, opusAlone) || strings.Contains(content, opusAlone+cMuted+"·") {
		t.Fatalf("row = %q, want the model alone", content)
	}
	if !strings.Contains(warned, "session effort") ||
		!strings.Contains(warned, sessionEffortPath(sidDir, effortSession)) {
		t.Fatalf("warn = %q, want the unreadable record named", warned)
	}
}
