package statusline

import (
	"bytes"
	"os"
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

func TestRecordSessionRecordsTheMainLineEffort(t *testing.T) {
	sidDir := t.TempDir()
	recordMainLine(t, sidDir, "claude-opus-5-5[1m]", "high")
	got, err := inheritedEffort(sidDir, effortSession)
	if err != nil || got != (sessionEffortRecord{Level: "high", Model: "claude-opus-5-5[1m]"}) {
		t.Fatalf("recorded effort = %+v err=%v, want high for claude-opus-5-5[1m]", got, err)
	}
}

func TestReadSessionReturnsWhatTheStatuslineRecorded(t *testing.T) {
	dir := t.TempDir()
	if got, err := ReadSession(dir, "no-such-session"); err != nil || got != (SessionRecord{}) {
		t.Fatalf("an unrecorded session is the zero record and no error, got %+v, %v", got, err)
	}
	if err := writeSessionEffort(
		dir,
		"sess-1",
		sessionEffortRecord{Level: "high", Model: "claude-opus-5-5"},
	); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSession(dir, "sess-1")
	if err != nil || got.Level != "high" || got.Model != "claude-opus-5-5" {
		t.Fatalf("ReadSession = %+v, %v", got, err)
	}
	if err := os.WriteFile(sessionEffortPath(dir, "broken"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSession(dir, "broken"); err == nil {
		t.Error("a record that cannot be parsed is an error, never an absent record")
	}
}
