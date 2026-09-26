package obs

import (
	"errors"
	"fmt"
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

// TestScrubRefusesCredentialShapedTextRegardlessOfSpelling is the F4 table:
// a credential planted in a declared key is refused whatever the scheme
// spelling, or a bare JWT — the second gate is not just sk-/bearer-/oauth-
// shaped.
func TestScrubRefusesCredentialShapedTextRegardlessOfSpelling(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"password=", "reason", "password=hunter2"},
		{"passwd=", "path", "passwd=hunter2"},
		{"token=", "argv", "token=abc123"},
		{"secret=", "reason", "secret=abc123"},
		{"api_key=", "path", "api_key=abc123"},
		{"apikey=", "argv", "apikey=abc123"},
		{"api-key", "reason", "the api-key rotated last night"},
		{"cookie:", "path", "cookie: sessionid=abc123"},
		{"set-cookie", "argv", "set-cookie: sessionid=abc123"},
		{"x-api-key", "reason", "x-api-key: abc123"},
		{
			"jwt shape", "path",
			"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Scrub(nil, slog.String(tt.key, tt.value))
			if got.Value.String() != redactedValue {
				t.Fatalf("Scrub(%s=%q) = %v, want refused", tt.key, tt.value, got.Value.String())
			}
		})
	}
}

// TestScrubKeepsOrdinaryTextThatOnlyResemblesAMarker is F4's negative
// control: the widened gate must not refuse a value merely for containing a
// marker's bare word — token= needs the =, not the bare word.
func TestScrubKeepsOrdinaryTextThatOnlyResemblesAMarker(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"tokenizer word", "reason", "the tokenizer split the input"},
		{"secretary word", "path", "ask the secretary for the file"},
		{"cookies filename", "path", "/var/lib/pfm/cache/cookies.txt"},
		{"plain file path", "path", "/var/lib/pfm/internal/obs/scrub.go"},
		{"normal argv", "argv", "git status --short"},
		{"monkey= is not key=", "path", "https://zoo.example/animals?monkey=1&turkey=roast"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Scrub(nil, slog.String(tt.key, tt.value))
			if got.Value.String() != tt.value {
				t.Fatalf("Scrub(%s=%q) = %v, want kept whole", tt.key, tt.value, got.Value.String())
			}
		})
	}
}

// TestScrubRefusesAnAnchoredAPIKeyQueryParameter is L2-F15's marker half: the
// Google Books API key shape (books.go:249, "...&key="+url.QueryEscape(...))
// is refused even though it carries none of the other marker substrings.
func TestScrubRefusesAnAnchoredAPIKeyQueryParameter(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"leading ?key=", "https://www.googleapis.com/books/v1/volumes?q=x&key=AIzaPLANTEDKEY", redactedValue},
		{"case-insensitive Key=", "https://example.test/?Key=PLANTEDKEY", redactedValue},
		{"x-subscription-token header", "X-Subscription-Token: brave-planted-token", redactedValue},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Scrub(nil, slog.String("path", tt.value))
			if got.Value.String() != tt.want {
				t.Fatalf("Scrub(path=%q) = %v, want %s", tt.value, got.Value.String(), tt.want)
			}
		})
	}
}

// TestScrubWalksNestedAnyValues is L1-F20/T-2: fmt.Sprint on a whole Any-kind
// value never re-walks nested keys on its own, so scrubValue must — a
// planted secret inside a []string, a map keyed by a secret-shaped name (not
// a "key=value" shaped value), or a wrapped error must never survive.
func TestScrubWalksNestedAnyValues(t *testing.T) {
	t.Run("slice of strings", func(t *testing.T) {
		ctx, recorder := Test(t)
		Logger(ctx).Info("probe", "argv", []string{"ok", "Bearer sk-PLANTEDSLICE"})
		if strings.Contains(recorder.Raw(), "PLANTEDSLICE") {
			t.Fatalf("a secret inside a []string leaked: %s", recorder.Raw())
		}
	})
	t.Run("map keyed by a secret-shaped name refuses the whole field", func(t *testing.T) {
		ctx, recorder := Test(t)
		// The value itself carries no "=" shape at all — only the key name
		// "password" marks it: fmt.Sprint of the map renders
		// "map[password:PLANTEDMAPVALUE]", never "password=PLANTEDMAPVALUE".
		// The whole field is refused, the same all-or-nothing rule a planted
		// secret inside a plain string already gets.
		Logger(ctx).Info("probe", "argv", map[string]string{"password": "PLANTEDMAPVALUE", "region": "us-east"})
		record := onlyRecord(t, recorder)
		wantField(t, record, "argv", redactedValue)
	})
	t.Run("a map with nothing secret-shaped survives whole", func(t *testing.T) {
		ctx, recorder := Test(t)
		Logger(ctx).Info("probe", "argv", map[string]string{"region": "us-east"})
		record := onlyRecord(t, recorder)
		wantField(t, record, "argv", "map[region:us-east]")
	})
	t.Run("wrapped error", func(t *testing.T) {
		ctx, recorder := Test(t)
		inner := errors.New("token=PLANTEDWRAPPED")
		wrapped := fmt.Errorf("harvester search failed: %w", inner)
		Logger(ctx).Info("probe", "argv", wrapped)
		if strings.Contains(recorder.Raw(), "PLANTEDWRAPPED") {
			t.Fatalf("a secret inside a wrapped error leaked: %s", recorder.Raw())
		}
	})
	t.Run("plain slice with nothing secret survives", func(t *testing.T) {
		ctx, recorder := Test(t)
		Logger(ctx).Info("probe", "argv", []string{"one", "two"})
		record := onlyRecord(t, recorder)
		wantField(t, record, "argv", "[one two]")
	})
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
