package obs

import (
	"context"
	"errors"
	"log/slog"
	"testing"
)

func TestWithScopesAChatSeatEngineAndSocketOntoEveryRecord(t *testing.T) {
	ctx, recorder := Test(t)
	scoped := With(ctx, FieldChat, "cc-3", FieldSeat, 2, FieldEngine, "claude", FieldSock, "cc-3.sock")
	Logger(scoped).Info("reload.begin")
	// The unscoped context keeps its own logger: scoping is additive, never a
	// global mutation.
	Logger(ctx).Info("unscoped")

	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2: %s", len(records), recorder.Raw())
	}
	for _, field := range []struct {
		key  string
		want any
	}{{FieldChat, "cc-3"}, {FieldSeat, float64(2)}, {FieldEngine, "claude"}, {FieldSock, "cc-3.sock"}} {
		got, found := records[0].Field(field.key)
		if !found || got != field.want {
			t.Fatalf("scoped record %s = %v (found %t), want %v", field.key, got, found, field.want)
		}
	}
	if _, found := records[1].Field(FieldChat); found {
		t.Fatalf("the unscoped record inherited a chat: %s", recorder.Raw())
	}
	if With(ctx) != ctx {
		t.Fatal("With(ctx) with no pairs returned a different context")
	}
}

func TestSpanLogsEntryExitDurationAndError(t *testing.T) {
	ctx, recorder := Test(t)
	end := Span(ctx, "reload.waitIdle")
	end(nil)
	failing := Span(ctx, "reload.inject")
	failing(errors.New("socket refused"))

	records := recorder.Records()
	if len(records) != 4 {
		t.Fatalf("records = %d, want 4 (start/end per span): %s", len(records), recorder.Raw())
	}
	wantMessages := []string{"reload.waitIdle.start", "reload.waitIdle.end", "reload.inject.start", "reload.inject.end"}
	for index, want := range wantMessages {
		if records[index].Message != want {
			t.Fatalf("record %d message = %q, want %q", index, records[index].Message, want)
		}
	}
	if _, found := records[1].Field(FieldDur); !found {
		t.Fatalf("a span's end record carries no %s: %s", FieldDur, recorder.Raw())
	}
	if records[3].Level != slog.LevelError.String() {
		t.Fatalf("a failed span ended at %s, want ERROR", records[3].Level)
	}
	if got, _ := records[3].Field(FieldErr); got != "socket refused" {
		t.Fatalf("failed span err = %v, want the error text", got)
	}
}

func TestLoggerWithoutAnOpenLogDiscardsInsteadOfPanicking(t *testing.T) {
	logger := Logger(context.Background())
	if logger == nil {
		t.Fatal("Logger returned nil before OpenLog")
	}
	logger.Info("nowhere")
	//nolint:staticcheck // a nil context is exactly the misuse this asserts against.
	if Logger(nil) == nil {
		t.Fatal("Logger(nil) returned nil instead of the process logger")
	}
}
