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
	return renderEffortAgentRow(t, sidDir, "", task)
}

// renderEffortAgentRow renders one task row for effortSession whose sub-agent
// transcripts sit beside the session transcript path.
func renderEffortAgentRow(t *testing.T, sidDir, session, task string) (content, warned string) {
	t.Helper()
	var warn bytes.Buffer
	payload := `{"session_id":"` + effortSession + `","transcript_path":` + jsonText(session) +
		`,"tasks":[` + task + `]}`
	got, err := RenderSubagents([]byte(payload), subagentNow, sidDir, testPrices(t), &warn)
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
	opusInherited = opusAlone + cMuted + "·" + reset + cEffort + "🏎️ high" + reset
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
	if !strings.Contains(content, opusAlone+cMuted+"·"+reset+cEffort+"🚲 low"+reset) {
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
	if want := opusAlone + cMuted + "·" + reset + cMuted + "🏎️ high" + reset; !strings.Contains(content, want) {
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

const agentTaskWithoutEffort = `{"id":"a","type":"local_agent","status":"running","model":"claude-opus-5-5",` +
	`"tokenCount":10,"contextWindowSize":100}`

// The effort a sub-agent's request went out at — after a plugin pinned it —
// is what its transcript records; its latest recorded level wins over the
// session's live effort, in the plain effort colour on any model, and an
// entry of a model that takes no effort leaves it standing.
func TestSubagentRowShowsItsTranscriptsLastRecordedEffort(t *testing.T) {
	session := subagentSession(t, map[string][]string{"a": {
		`{"type":"user","timestamp":"2026-09-24T10:00:00Z","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-09-24T10:00:05Z","message":{"content":[]},` +
			`"effort":"low","perTurnEffort":null}`,
		`{"type":"assistant","timestamp":"2026-09-24T10:00:10Z","message":{"content":[]},` +
			`"effort":"medium","perTurnEffort":null}`,
		`{"type":"assistant","timestamp":"2026-09-24T10:00:15Z","message":{"content":[]}}`,
		`{"type":"assistant","timestamp":"2026-09-24T10:00:20Z","message":{"content":[]},"effort":""}`,
	}}, map[string]string{"a": "tracer"})
	for _, sessionModel := range []string{"claude-opus-5-5[1m]", "claude-sonnet-5[1m]"} {
		t.Run(sessionModel, func(t *testing.T) {
			sidDir := t.TempDir()
			recordMainLine(t, sidDir, sessionModel, "xhigh")
			content, warned := renderEffortAgentRow(t, sidDir, session, agentTaskWithoutEffort)
			want := opusAlone + cMuted + "·" + reset + cEffort + "🏍️ medium" + reset
			if !strings.Contains(content, want) || warned != "" {
				t.Fatalf("row = %q warn=%q, want the transcript's last recorded effort %q", content, warned, want)
			}
		})
	}
}

// Before its first request records an effort the row falls back to the
// session's effort, exactly as a row without a transcript.
func TestSubagentRowWithoutARecordedEffortShowsTheSessionEffort(t *testing.T) {
	session := subagentSession(t, map[string][]string{"a": {
		`{"type":"user","timestamp":"2026-09-24T10:00:00Z","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-09-24T10:00:05Z","message":{"content":[]}}`,
	}}, map[string]string{"a": "tracer"})
	sidDir := t.TempDir()
	recordMainLine(t, sidDir, "claude-opus-5-5[1m]", "xhigh")
	content, warned := renderEffortAgentRow(t, sidDir, session, agentTaskWithoutEffort)
	if want := opusAlone + cMuted + "·" + reset + cEffort + "🚀 xhigh" + reset; !strings.Contains(content, want) ||
		warned != "" {
		t.Fatalf("row = %q warn=%q, want the session effort %q", content, warned, want)
	}
}
