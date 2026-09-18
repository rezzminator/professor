package obs

import (
	"log/slog"
	"strings"
	"testing"
)

// TestScrubRefusesCredentialsTokensPromptsAndTranscriptLines is THE redaction
// test the wave-6 spec pins: "Never a credential, a token, a prompt body, or a
// transcript line". Each is built into a record through the same handler the
// home's pfm.jsonl uses, and none of them may appear in what was written.
func TestScrubRefusesCredentialsTokensPromptsAndTranscriptLines(t *testing.T) {
	ctx, recorder := Test(t)
	const (
		credential = "sk-ant-api03-SECRETKEYMATERIAL"
		token      = "Bearer eyJhbGciOiJIUzI1NiJ9.PAYLOAD.SIGNATURE"
		oauth      = "claudeAiOauth{refresh:REFRESHME}"
	)
	prompt := "You are a helpful assistant. " + strings.Repeat("prompt-body ", 60)
	transcript := strings.Repeat("assistant: the user's private transcript line ", 20)

	Logger(ctx).Info("probe",
		FieldChat, "cc-7",
		"secret", credential,
		"authorization", token,
		FieldErr, "refresh failed: "+oauth,
		"prompt", prompt,
		"transcript", transcript,
	)

	written := recorder.Raw()
	for name, leaked := range map[string]string{
		"credential": credential,
		"token":      token,
		"oauth":      oauth,
		"prompt":     "prompt-body",
		"transcript": "private transcript line",
	} {
		if strings.Contains(written, leaked) {
			t.Fatalf("%s leaked into the activity log: %s", name, written)
		}
	}
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: %s", len(records), written)
	}
	// The refused fields are still THERE, marked refused: a redacted field that
	// vanished would read as a call site that never logged it.
	for _, key := range []string{"secret", "authorization", "prompt", "transcript", FieldErr} {
		value, found := records[0].Field(key)
		if !found {
			t.Fatalf("field %q disappeared instead of being refused: %s", key, written)
		}
		if text, _ := value.(string); !strings.HasPrefix(text, "<redacted") {
			t.Fatalf("field %q = %v, want a <redacted...> marker", key, value)
		}
	}
	if chat, _ := records[0].Field(FieldChat); chat != "cc-7" {
		t.Fatalf("declared field chat = %v, want cc-7", chat)
	}
}

func TestScrubRefusesUndeclaredKeysAndCapsOversizeValues(t *testing.T) {
	if Declared("nobody-declared-this") {
		t.Fatal("an undeclared key reported as declared")
	}
	for _, key := range []string{FieldCmd, FieldChat, FieldSeat, FieldEngine, FieldSock, FieldDur, FieldErr} {
		if !Declared(key) {
			t.Fatalf("spec field %q is not on the allow-list", key)
		}
	}
	oversize := strings.Repeat("x", MaxValueBytes+1)
	got := Scrub(nil, slog.String(FieldErr, oversize))
	if want := oversize[:MaxValueBytes] + "…(truncated 1 bytes)"; got.Value.String() != want {
		t.Fatalf(
			"oversize value = len %d, want the %d byte head plus the marker", len(got.Value.String()), MaxValueBytes,
		)
	}
	fits := strings.Repeat("x", MaxValueBytes)
	if kept := Scrub(nil, slog.String(FieldErr, fits)); kept.Value.String() != fits {
		t.Fatalf("a %d byte value was not kept whole", MaxValueBytes)
	}
}

func TestScrubRenamesTimeToTS(t *testing.T) {
	ctx, recorder := Test(t)
	Logger(ctx).Info("probe")
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if _, found := records[0].Field(FieldTime); !found {
		t.Fatalf("record carries no %s field: %s", FieldTime, recorder.Raw())
	}
	if _, found := records[0].Field(slog.TimeKey); found {
		t.Fatalf("record still carries slog's %q key: %s", slog.TimeKey, recorder.Raw())
	}
}

// TestScrubAtDebugWithTheShapeFields re-runs the redaction law at DEBUG with
// every field a middleware reports (spec § Middleware, brief A5): the planted
// secrets never land, whichever declared key carries them, and every shape key
// Lanes B and C use is declared.
func TestScrubAtDebugWithTheShapeFields(t *testing.T) {
	ctx, recorder := Test(t)
	shape := []string{
		"argc", "host", "path", "status", "bytes", "rows", "table", "kind", "op", "tool", "route", "method", "hook",
		"decision", "reason", "prior", "next", "cause", "subcmd", "target", "retries", "pid", FieldComp,
	}
	for _, key := range shape {
		if !Declared(key) {
			t.Fatalf("shape field %q is not declared", key)
		}
	}
	const (
		key    = "sk-ant-api03-PLANTEDKEY"
		bearer = "Bearer PLANTEDTOKEN.SIG"
		header = "Authorization: Basic UExBTlRFRA=="
		prompt = "You are a helpful assistant; the user's planted prompt body"
	)
	Logger(Component(ctx, "http.out")).Debug("http.out.request",
		"path", "/v1/messages?key="+key,
		"reason", bearer,
		"target", header,
		"authorization", header,
		"prompt", prompt,
		"body", prompt,
		"argc", 3,
	)
	written := recorder.Raw()
	for name, leaked := range map[string]string{
		"key": "PLANTEDKEY", "bearer": "PLANTEDTOKEN", "header": "UExBTlRFRA", "prompt": "planted prompt body",
	} {
		if strings.Contains(written, leaked) {
			t.Fatalf("%s leaked at DEBUG: %s", name, written)
		}
	}
	records := recorder.Records()
	if len(records) != 1 || records[0].Level != "DEBUG" {
		t.Fatalf("records = %+v, want one DEBUG record", records)
	}
	for _, refused := range []string{"authorization", "prompt", "body"} {
		if value, _ := records[0].Field(refused); value != redactedValue {
			t.Fatalf("undeclared %q = %v, want %s", refused, value, redactedValue)
		}
	}
	if argc, _ := records[0].Field("argc"); argc != float64(3) {
		t.Fatalf("argc = %v, want the shape to survive", argc)
	}
	if comp, _ := records[0].Field(FieldComp); comp != "http.out" {
		t.Fatalf("comp = %v, want http.out", comp)
	}
}
