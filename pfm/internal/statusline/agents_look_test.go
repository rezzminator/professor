package statusline

import (
	"reflect"
	"testing"
)

// An agent's text is its facts in the main line's units and symbols, and the
// look hands each effort level the colour the main line's bar wears.
func TestNewAgentText(t *testing.T) {
	cases := []struct {
		name  string
		facts AgentFacts
		want  AgentText
	}{
		{
			name: "a priced opus agent at high effort",
			facts: AgentFacts{
				Model: "claude-opus-5-5", Effort: "high", Context: 69_100,
				Spend: &AgentSpend{USD: 1.235, USDKnown: true, In: 3_100_000, Out: 42_000},
			},
			want: AgentText{
				Glyph: "◆", Family: "opus", EffortBar: "▰▰▰", Context: "69.1K",
				In: "3.1M", Out: "42.0K", USD: "$1.24",
			},
		},
		{
			name:  "unknown model, no effort, nothing priced",
			facts: AgentFacts{Spend: &AgentSpend{In: 10}},
			want:  AgentText{Context: "0", In: "10", Out: "0"},
		},
		{
			name:  "an effort outside the five has no bar; no spend leaves no counts",
			facts: AgentFacts{Model: "claude-opus-5-5", Effort: "auto"},
			want:  AgentText{Glyph: "◆", Family: "opus", Context: "0"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := newAgentText(&tc.facts); got != tc.want {
				t.Fatalf("newAgentText = %+v, want %+v", got, tc.want)
			}
		})
	}
	look := agentsLook()
	pulseWant := AgentsPulse{
		Glyphs: []string{"_", "⎽", "⎼", "⎻", "⎺", "¯"}, Drop: "↓",
		Steps: []int64{1, 1_000, 5_000, 20_000, 50_000}, DropFrom: 1_000, StepMs: 5_000,
	}
	if got := look.Pulse; !reflect.DeepEqual(got, pulseWant) {
		t.Errorf("look pulse = %+v, want %+v", got, pulseWant)
	}
	tonesWant := AgentsTones{
		USD:      AgentsTone{Warn: 2, Bad: 10},
		Share:    AgentsTone{Warn: 50, Bad: 80},
		CacheHit: AgentsHitTone{Warn: 50, Good: 80},
	}
	if look.Tones != tonesWant {
		t.Errorf("look tones = %+v, want %+v", look.Tones, tonesWant)
	}
	colours := look.EffortColors
	for level, want := range map[string]string{
		"low": "\x1b[38;5;117m", "medium": "\x1b[38;5;118m", "high": "\x1b[38;5;226m",
		"xhigh": "\x1b[38;5;215m", "max": "\x1b[1;38;5;203m",
	} {
		if colours[level] != want {
			t.Errorf("effort colour of %s = %q, want %q", level, colours[level], want)
		}
	}
}
