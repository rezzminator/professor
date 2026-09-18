package inject

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
	"hostops/pfm/internal/resolve"
)

// stateRecords returns the comp=state records for coordinator kind.
func stateRecords(recorder *obs.Recorder, kind string) []obs.Record {
	var out []obs.Record
	for _, record := range recorder.Records() {
		got, _ := record.Field("kind")
		if record.Message == "state.transition" && got == kind {
			out = append(out, record)
		}
	}
	return out
}

// TestInjectAndSelfCompactRecordTheirOutcomeAsATransition: the inject and
// self-compact coordinators are state doors — one comp=state record per call
// from `requested` to the outcome (typed, or refused with the code as
// cause), and the message text never reaches the file.
func TestInjectAndSelfCompactRecordTheirOutcomeAsATransition(t *testing.T) {
	ctx, recorder := obs.Test(t)
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cc-state-door", fake, spawner)
	result, err := engine.Inject(ctx, Request{Target: "chat", Message: "/compact PLANTED-hold: read /tmp/hold.md"})
	if err != nil || result.Code != CodeUndelivered {
		t.Fatalf("Inject() = %+v, %v", result, err)
	}
	records := stateRecords(recorder, "inject")
	if len(records) != 1 {
		t.Fatalf("inject transitions = %d, want 1: %s", len(records), recorder.Raw())
	}
	for key, want := range map[string]any{obs.FieldComp: "state", "prior": "requested", "next": "refused", "cause": "code 6"} {
		if got, _ := records[0].Field(key); got != want {
			t.Fatalf("%s = %v, want %v", key, got, want)
		}
	}
	if _, found := records[0].Field(obs.FieldDur); !found {
		t.Fatalf("no dur_ms: %v", records[0].Fields)
	}

	engine.whoami = fakeSelf{identity: resolve.Identity{
		Session:    "self-session",
		SocketPath: filepath.Join(string(filepath.Separator), "tmp", "tmux-jail", "cc-state-door"),
		Pane:       "%1",
	}}
	if result, err := engine.ScheduleSelfCompact(
		ctx,
		"line one\nPLANTED two",
		[]string{"resume"},
	); err != nil ||
		result.Code != CodeUndelivered {
		t.Fatalf("ScheduleSelfCompact() = %+v, %v", result, err)
	}
	compact := stateRecords(recorder, "self-compact")
	if len(compact) != 1 {
		t.Fatalf("self-compact transitions = %d, want 1: %s", len(compact), recorder.Raw())
	}
	if next, _ := compact[0].Field("next"); next != "refused" {
		t.Fatalf("next = %v, want refused", next)
	}
	if strings.Contains(recorder.Raw(), "PLANTED") {
		t.Fatalf("message text reached the file: %s", recorder.Raw())
	}
	_ = context.Background
}

// TestDeliverThenRecordsItsOutcomeAsATransition: the then-delivery
// coordinator refusing an empty chain is one requested → refused record.
func TestDeliverThenRecordsItsOutcomeAsATransition(t *testing.T) {
	ctx, recorder := obs.Test(t)
	engine := newTestEngineWith(t, "cc-then-door", &fakeTmux{capture: "❯ "}, &fakeSpawner{})
	if result, err := engine.DeliverThen(ctx, "", "%1", nil, false); err != nil || result.Code != 1 {
		t.Fatalf("DeliverThen() = %+v, %v", result, err)
	}
	records := stateRecords(recorder, "then")
	if len(records) != 1 {
		t.Fatalf("then transitions = %d, want 1: %s", len(records), recorder.Raw())
	}
	if next, _ := records[0].Field("next"); next != "refused" {
		t.Fatalf("next = %v", next)
	}
}
