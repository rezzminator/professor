package digest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseReadsRecordShapesAndTallies(t *testing.T) {
	engines := []struct{ raw, want string }{
		{`{"type":"session_meta","payload":{}}`, "codex"},
		{`{"type":"event_msg"}`, "codex"},
		{`{"type":"assistant","message":{}}`, "claude"},
		{`{"type":"something","sessionId":"s"}`, "claude"},
		{`{"type":"something"}`, ""},
		{`[1,2]`, ""},
	}
	for _, row := range engines {
		if got := detectEngine([]byte(row.raw)); got != row.want {
			t.Errorf("detectEngine(%s) = %q, want %q", row.raw, got, row.want)
		}
	}
	if got := member(json.RawMessage(`{"a":1,"b":2,"a":3}`), "a"); string(got) != "3" {
		t.Errorf("member of a repeated key = %s, want the last value", got)
	}
	if got := member(json.RawMessage(`[1]`), "a"); got != nil {
		t.Errorf("member of a list = %s, want none", got)
	}
	for _, row := range []struct{ raw, want string }{{`"user"`, "user"}, {`""`, "?"}, {`null`, "?"}, {`7`, "7"}} {
		if got := typeLabel(json.RawMessage(row.raw)); got != row.want {
			t.Errorf("typeLabel(%s) = %q, want %q", row.raw, got, row.want)
		}
	}
	counter := newTally()
	for _, key := range []string{"Read", "Bash", "Bash", "Grep", "Read", "Bash"} {
		counter.add(key)
	}
	if got := counter.mostCommon(); !slices.Equal(got, []string{"Bash", "Read", "Grep"}) {
		t.Errorf("mostCommon = %q", got)
	}
	if counter.total() != 6 {
		t.Errorf("total = %d, want 6", counter.total())
	}
}

// TestCodexNullSettingsReadAsAbsent: a null model, effort or cwd is Python's
// None — absent to the header's model and to the ./ shortening.
func TestCodexNullSettingsReadAsAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-2026-09-29T20-00-00-s1.jsonl")
	lines := `{"timestamp":"2026-09-29T20:00:00.000Z","type":"session_meta","payload":{"id":"s1","cwd":null}}` + "\n" +
		`{"timestamp":"2026-09-29T20:00:01.000Z","type":"turn_context","payload":{"model":"gpt-5","effort":null,` +
		`"collaboration_mode":{"settings":{"reasoning_effort":null}}}}` + "\n" +
		`{"timestamp":"2026-09-29T20:00:02.000Z","type":"response_item","payload":{"type":"message","role":"user",` +
		`"content":[{"type":"input_text","text":"open None/x"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := DigestTranscript(context.Background(), []string{"show", path}, Env{})
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := strings.Cut(out.Text, "\n")
	if head != "TRANSCRIPT codex · s1 · gpt-5" || !strings.Contains(out.Text, "cwd None (shown as ./)") ||
		!strings.Contains(out.Text, "PROMPT open None/x") {
		t.Errorf("digest = %q", out.Text)
	}
}
