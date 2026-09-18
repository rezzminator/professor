package obs

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestHookRecordsNameDecisionReasonAndDuration: the hooks door writes one
// comp=hooks record per dispatched internal subcommand — the hook's name,
// the decision (from the hook's own JSON answer when it wrote one, else
// from its exit code), the reason, dur_ms — and the hook's stdout still
// reaches the caller byte for byte.
func TestHookRecordsNameDecisionReasonAndDuration(t *testing.T) {
	ctx, recorder := Test(t)
	var stdout bytes.Buffer
	tee, finish := Hook(ctx, "exit-intercept", &stdout)
	if _, err := tee.Write([]byte(`{"decision":"block","reason":"another reload is in flight"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	finish(0)
	if !strings.HasPrefix(stdout.String(), `{"decision":"block"`) {
		t.Fatalf("the hook's stdout did not reach the caller: %q", stdout.String())
	}
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: %s", len(records), recorder.Raw())
	}
	record := records[0]
	if record.Message != "hooks.run" || record.Level != slog.LevelInfo.String() {
		t.Fatalf("record = %s at %s", record.Message, record.Level)
	}
	requireField(t, record, FieldComp, "hooks")
	requireField(t, record, "hook", "exit-intercept")
	requireField(t, record, "decision", "block")
	requireField(t, record, "reason", "another reload is in flight")
	requireField(t, record, FieldExit, float64(0))
	requireDur(t, record)
}

// TestHookDecisionFromExitCodeAndPermissionShape: no JSON answer → the exit
// code decides (0 allow, 2 block, else error at ERROR level); Claude Code's
// PreToolUse permission shape is read too.
func TestHookDecisionFromExitCodeAndPermissionShape(t *testing.T) {
	ctx, recorder := Test(t)
	_, finish := Hook(ctx, "launch", &bytes.Buffer{})
	finish(0)
	_, finish = Hook(ctx, "clear-kill", &bytes.Buffer{})
	finish(2)
	_, finish = Hook(ctx, "then", &bytes.Buffer{})
	finish(1)
	tee, finish := Hook(ctx, "explore-deny", &bytes.Buffer{})
	_, _ = tee.Write([]byte(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Explore is read-only"}}`))
	finish(0)
	records := recorder.Records()
	if len(records) != 4 {
		t.Fatalf("records = %d, want 4: %s", len(records), recorder.Raw())
	}
	requireField(t, records[0], "decision", "allow")
	requireField(t, records[1], "decision", "block")
	requireField(t, records[2], "decision", "error")
	if records[2].Level != slog.LevelError.String() || records[1].Level != slog.LevelInfo.String() {
		t.Fatalf("levels: exit 1 at %s (want ERROR), exit 2 at %s (want INFO)", records[2].Level, records[1].Level)
	}
	requireField(t, records[3], "decision", "deny")
	requireField(t, records[3], "reason", "Explore is read-only")
	if _, found := records[0].Field("reason"); found {
		t.Fatalf("a hook that gave no reason got one: %v", records[0].Fields)
	}
}

// TestHookCapturesOnlyAHeadOfStdout: a hook streaming more than the capture
// cap (a statusline render, a picker listing) is passed through whole while
// the record keeps no more than the cap — and non-JSON stdout yields no
// reason at all, never a fragment of output.
func TestHookCapturesOnlyAHeadOfStdout(t *testing.T) {
	ctx, recorder := Test(t)
	var stdout bytes.Buffer
	tee, finish := Hook(ctx, "statusline", &stdout)
	big := strings.Repeat("render PLANTED-LINE\n", 1000)
	if _, err := tee.Write([]byte(big)); err != nil {
		t.Fatal(err)
	}
	finish(0)
	if stdout.Len() != len(big) {
		t.Fatalf("passed through %d bytes, want %d", stdout.Len(), len(big))
	}
	record := recorder.Records()[0]
	if _, found := record.Field("reason"); found || strings.Contains(recorder.Raw(), "PLANTED") {
		t.Fatalf("non-JSON stdout leaked into the record: %s", recorder.Raw())
	}
}
