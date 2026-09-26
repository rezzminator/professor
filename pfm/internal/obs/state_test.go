package obs

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// TestTransitionRecordsPriorNextCauseAndResult: one comp=state record per
// transition — the coordinator under kind, prior, next, cause, dur_ms — INFO
// when it completed, ERROR with err when it failed.
func TestTransitionRecordsPriorNextCauseAndResult(t *testing.T) {
	ctx, recorder := Test(t)
	Transition(ctx, "reload", "locked", "idle", "caller turn ended")(nil)
	Transition(ctx, "kill", "live", "killed", "operator --self")(errors.New("pane already gone"))
	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want one per transition: %s", len(records), recorder.Raw())
	}
	first := records[0]
	if first.Message != "state.transition" || first.Level != slog.LevelInfo.String() {
		t.Fatalf("record = %s at %s", first.Message, first.Level)
	}
	for key, want := range map[string]any{FieldComp: "state", "kind": "reload", "prior": "locked", "next": "idle", "cause": "caller turn ended"} {
		requireField(t, first, key, want)
	}
	requireDur(t, first)
	if records[1].Level != slog.LevelError.String() {
		t.Fatalf("failed transition at %s, want ERROR", records[1].Level)
	}
	requireField(t, records[1], FieldErr, "pane already gone")
	requireField(t, records[1], "next", "killed")
}

// TestTrailWalksACoordinatorsStates: a Trail is the linear coordinator's
// ledger — every Reach is one transition from the state before it, End
// closes the last state as done or failed, and a prompt handed over as a
// cause never reaches the file (Scrub refuses it, the trail records shapes).
func TestTrailWalksACoordinatorsStates(t *testing.T) {
	ctx, recorder := Test(t)
	trail := NewTrail(ctx, "reload", "requested")
	trail.Reach("locked", "pane mutex held")
	trail.Reach("exit-typed", "/exit sent")
	trail.End(nil)
	failing := NewTrail(ctx, "inject", "resolved")
	failing.Reach("typed", "bearer sk-PLANTED prompt body")
	failing.End(errors.New("pane in copy-mode"))
	records := recorder.Records()
	want := []struct{ prior, next string }{
		{"requested", "locked"},
		{"locked", "exit-typed"},
		{"exit-typed", "done"},
		{"resolved", "typed"},
		{"typed", "failed"},
	}
	if len(records) != len(want) {
		t.Fatalf("records = %d, want %d: %s", len(records), len(want), recorder.Raw())
	}
	for index, step := range want {
		requireField(t, records[index], "prior", step.prior)
		requireField(t, records[index], "next", step.next)
		requireDur(t, records[index])
	}
	requireField(t, records[0], "kind", "reload")
	requireField(t, records[4], FieldErr, "pane in copy-mode")
	if records[4].Level != slog.LevelError.String() || records[2].Level != slog.LevelInfo.String() {
		t.Fatalf("levels: failed end at %s, done end at %s", records[4].Level, records[2].Level)
	}
	if strings.Contains(recorder.Raw(), "PLANTED") {
		t.Fatalf("a credential-looking cause reached the file: %s", recorder.Raw())
	}
	// A trail ended twice writes nothing more.
	failing.End(nil)
	if len(recorder.Records()) != len(want) {
		t.Fatalf("a second End wrote a record: %s", recorder.Raw())
	}
}
