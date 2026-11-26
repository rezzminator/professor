package herdr

import (
	"strings"
	"testing"
)

func TestReportLineIsExactlyHerdrsRequest(t *testing.T) {
	line, err := Report{
		Pane: "w1:p1", Agent: "claude", State: StateBlocked, Message: "needs\napproval",
		SessionID: "s1", ResumeArgv: []string{"pfm", "chat", "open", "s1"}, Seq: 42,
	}.Line()
	want := `{"id":"pfm-42","method":"pane.report_agent","params":{"pane_id":"w1:p1","source":"pfm","agent":"claude","state":"blocked","message":"needs approval","agent_session_id":"s1","resume_argv":["pfm","chat","open","s1"],"seq":42}}`
	if err != nil || string(line) != want {
		t.Fatalf("Line = %s, %v\nwant   %s", line, err, want)
	}
	bare, err := Report{Pane: "w1:p1", Agent: "codex", State: StateIdle, Seq: 7}.Line()
	wantBare := `{"id":"pfm-7","method":"pane.report_agent","params":{"pane_id":"w1:p1","source":"pfm","agent":"codex","state":"idle","seq":7}}`
	if err != nil || string(bare) != wantBare {
		t.Fatalf("Line = %s, %v\nwant   %s", bare, err, wantBare)
	}
	shell, err := Report{Pane: "w1:p3", Source: "pfm-shell", Agent: "shell", State: StateWorking, Seq: 8}.Line()
	wantShell := `{"id":"pfm-8","method":"pane.report_agent","params":{"pane_id":"w1:p3","source":"pfm-shell","agent":"shell","state":"working","seq":8}}`
	if err != nil || string(shell) != wantShell {
		t.Fatalf("Line = %s, %v\nwant   %s", shell, err, wantShell)
	}
	for _, report := range []Report{{Agent: "a", State: StateIdle}, {Pane: "p", State: StateIdle}, {Pane: "p", Agent: "a"}} {
		if line, err := report.Line(); err == nil {
			t.Errorf("Line(%+v) = %s, want an error for a missing pane, agent or state", report, line)
		}
	}
}

func TestSessionReportLineIsExactlyHerdrsRequest(t *testing.T) {
	line, err := (SessionReport{Pane: "w1:p1", Source: "herdr:claude", Agent: "claude", SessionID: "s1", Seq: 43}).Line()
	want := `{"id":"pfm-43","method":"pane.report_agent_session","params":{"pane_id":"w1:p1","source":"herdr:claude","agent":"claude","agent_session_id":"s1","seq":43}}`
	if err != nil || string(line) != want {
		t.Fatalf("session wire=%s err=%v want=%s", line, err, want)
	}
	line, err = (SessionReport{
		Pane: "w1:p1", Source: "herdr:claude", Agent: "claude", SessionID: "s2", StartSource: "resume", Seq: 45,
	}).Line()
	want = `{"id":"pfm-45","method":"pane.report_agent_session","params":{"pane_id":"w1:p1","source":"herdr:claude",` +
		`"agent":"claude","agent_session_id":"s2","session_start_source":"resume","seq":45}}`
	if err != nil || string(line) != want {
		t.Fatalf("resume session wire=%s err=%v want=%s", line, err, want)
	}
	for _, report := range []SessionReport{{Pane: "p"}, {Agent: "claude"}} {
		if _, err := report.Line(); err == nil {
			t.Fatalf("accepted incomplete session report: %+v", report)
		}
	}
	line, err = (SessionReport{Pane: "p", Agent: "claude", Source: "other", Seq: 44}).Line()
	if err != nil || strings.Contains(string(line), "agent_session_id") || strings.Contains(string(line), "state") ||
		!strings.Contains(string(line), `"source":"other"`) {
		t.Fatalf("optional session wire=%s err=%v", line, err)
	}
}

func TestReleaseLineIsExactlyHerdrsRequest(t *testing.T) {
	line, err := Release{Pane: "w1:p1", Agent: "opencode", Seq: 9}.Line()
	want := `{"id":"pfm-9","method":"pane.release_agent","params":{"pane_id":"w1:p1","source":"pfm","agent":"opencode","seq":9}}`
	if err != nil || string(line) != want {
		t.Fatalf("Line = %s, %v\nwant   %s", line, err, want)
	}
	shell, err := Release{Pane: "w1:p3", Source: "pfm-shell", Agent: "shell", Seq: 10}.Line()
	wantShell := `{"id":"pfm-10","method":"pane.release_agent","params":{"pane_id":"w1:p3","source":"pfm-shell","agent":"shell","seq":10}}`
	if err != nil || string(shell) != wantShell {
		t.Fatalf("Line = %s, %v\nwant   %s", shell, err, wantShell)
	}
	if line, err := (Release{Agent: "a"}).Line(); err == nil {
		t.Errorf("Line without a pane = %s, want an error", line)
	}
}

func TestMessageIsCappedAtTwoHundredFortyRunes(t *testing.T) {
	line, err := Report{Pane: "p", Agent: "a", State: StateBlocked, Message: strings.Repeat("é", 500), Seq: 1}.Line()
	if err != nil ||
		string(
			line,
		) != "{\"id\":\"pfm-1\",\"method\":\"pane.report_agent\",\"params\":{\"pane_id\":\"p\",\"source\":\"pfm\",\"agent\":\"a\",\"state\":\"blocked\",\"message\":\"ééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééé…\",\"seq\":1}}" {
		t.Fatalf("Line = %s, %v, want 239 runes and an ellipsis", line, err)
	}
}
