package mockengine

import (
	"strings"
	"testing"
)

func TestInlineDirectiveParser(t *testing.T) {
	for _, row := range []struct {
		name, prompt    string
		count           int
		refused, absent bool
	}{
		{"no marker", "plain prompt", 0, false, true},
		{"object", `hi mock-engine: {"type":"turn","reply":"R1"}`, 1, false, false},
		{"array", `mock-engine: [{"type":"background_agent","name":"a"},{"type":"turn","reply":"R2"}]`, 2, false, false},
		{"empty array", `mock-engine: []`, 0, false, false},
		{"first line", "mock-engine: {\"type\":\"turn\"}\nnext line", 1, false, false},
		{"compact", `mock-engine: {"type":"compact"}`, 1, false, false},
		{"unknown type", `mock-engine: {"type":"nope"}`, 0, true, false},
		{"unknown field", `mock-engine: {"type":"turn","oops":true}`, 0, true, false},
		{"broken JSON", `mock-engine: {"type":`, 0, true, false},
		{"trailing object", `mock-engine: {"type":"turn"} {}`, 0, true, false},
		{"unsigned footer", `mock-engine: {"type":"turn","reply":"R"} — UNSIGNED — sender identity underivable; no session id`, 1, false, false},
		{"signed footer", `mock-engine: [{"type":"turn","reply":"R"}] — sid 1234abcd · to reply: call lane <message>`, 1, false, false},
		{"force delivered footer", `mock-engine: {"type":"turn","reply":"R"} — ⚠ FORCE-DELIVERED via Esc (…) — sid 1234abcd`, 1, false, false},
		{"trailing garbage", `mock-engine: {"type":"turn"} garbage`, 0, true, false},
		{"null", `mock-engine: null`, 0, true, false},
		{"hold without gate", `mock-engine: {"type":"hold"}`, 0, true, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			steps, err := parseInlineSteps(row.prompt)
			if row.refused {
				if err == nil {
					t.Fatalf("bad directive accepted: %+v", steps)
				}
				if row.name == "trailing garbage" {
					reply := (&script{}).forPrompt(row.prompt).Steps[0].Reply
					if !strings.HasPrefix(reply, "mock-engine: inline steps refused — ") {
						t.Fatalf("refusal reply = %q", reply)
					}
				}
				return
			}
			if err != nil || len(steps) != row.count || (steps == nil) != row.absent {
				t.Fatalf("steps=%+v err=%v, want count=%d absent=%v", steps, err, row.count, row.absent)
			}
			if strings.Contains(row.name, "footer") && steps[0].Reply != "R" {
				t.Fatalf("footer step reply = %q, want R", steps[0].Reply)
			}
		})
	}
}
