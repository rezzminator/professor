package chat

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestRecordVerbRecordsSuccessAndFailure: RecordVerb walks the chat state
// door (spec § Middleware, `state`) for a CLI verb's outcome — the
// registered prior -> next pair, INFO on success (code == 0), ERROR naming
// the verb and exit code on failure — never a resolved chat's id or socket.
func TestRecordVerbRecordsSuccessAndFailure(t *testing.T) {
	ctx, recorder := obs.Test(t)
	RecordVerb(ctx, "kill", 0)
	RecordVerb(ctx, "kill", 1)

	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2: %s", len(records), recorder.Raw())
	}

	success := records[0]
	if success.Message != "state.transition" {
		t.Fatalf("success message = %q, want state.transition", success.Message)
	}
	if level := success.Level; level != "INFO" {
		t.Fatalf("success level = %q, want INFO", level)
	}
	if kind, _ := success.Field("kind"); kind != "chat" {
		t.Fatalf("success kind = %v, want chat", kind)
	}
	if prior, _ := success.Field("prior"); prior != "live" {
		t.Fatalf("success prior = %v, want live", prior)
	}
	if next, _ := success.Field("next"); next != "killed" {
		t.Fatalf("success next = %v, want killed", next)
	}

	failure := records[1]
	if level := failure.Level; level != "ERROR" {
		t.Fatalf("failure level = %q, want ERROR", level)
	}
	if prior, _ := failure.Field("prior"); prior != "live" {
		t.Fatalf("failure prior = %v, want live", prior)
	}
	if next, _ := failure.Field("next"); next != "killed" {
		t.Fatalf("failure next = %v, want killed", next)
	}
	if errField, found := failure.Field(obs.FieldErr); !found || errField != "chat kill exited 1" {
		t.Fatalf("failure err = %v (found=%v), want %q", errField, found, "chat kill exited 1")
	}

	// An unregistered verb is a no-op: no mis-shaped transition.
	RecordVerb(ctx, "does-not-exist", 0)
	if len(recorder.Records()) != 2 {
		t.Fatalf("records after unknown verb = %d, want 2 (no-op): %s", len(recorder.Records()), recorder.Raw())
	}
}
