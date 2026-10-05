package modelglyph

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestSymbolReadsADisplayNameOrAnIDInAnyCase(t *testing.T) {
	for model, want := range map[string]string{
		"Fable 5.1":         "✦",
		"Opus 4":            "◆",
		"claude-opus-4-7":   "◆",
		"CLAUDE-SONNET-5-5": "◇",
		"Haiku":             "○",
		"gpt-6-astra":       Unknown,
		"":                  Unknown,
	} {
		if got := Symbol(model); got != want {
			t.Errorf("Symbol(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestListSymbolKeepsTheFamiliesAndNeverSpendsTheLiveDot(t *testing.T) {
	if got := ListSymbol("claude-opus-4-7"); got != "◆" {
		t.Errorf("a known family keeps its symbol, got %q", got)
	}
	if got := ListSymbol("gpt-6-astra"); got != "◈" || got == Unknown {
		t.Errorf("an unknown family wears the diamond, got %q", got)
	}
}

func TestEffortLabelWearsTheStatusLineEmoji(t *testing.T) {
	for level, want := range map[string]string{
		"low": "🚲 low", "medium": "🏍️ medium", "high": "🏎️ high", "xhigh": "🚀 xhigh", "max": "🛰️ max",
		"turbo": "🔆 turbo",
	} {
		if got := EffortLabel(level); got != want {
			t.Errorf("EffortLabel(%q) = %q, want %q", level, got, want)
		}
	}
	if EffortEmoji("") != "" || RowEffort("") != "" {
		t.Error("no level, no emoji")
	}
}

// Every row emoji must be exactly two cells to the program and carry no
// variation selector, which is what lets a terminal agree with it.
func TestRowEffortIsWidthSafeForEveryLevel(t *testing.T) {
	seen := map[string]string{}
	for _, level := range []string{"low", "medium", "high", "xhigh", "max", "turbo"} {
		emoji := RowEffort(level)
		if got := ansi.StringWidth(emoji); got != 2 {
			t.Errorf("RowEffort(%q) = %q is %d cells, want 2", level, emoji, got)
		}
		if len([]rune(emoji)) != 1 {
			t.Errorf("RowEffort(%q) = %q must be one code point: a variation selector desyncs terminals", level, emoji)
		}
		if other, dup := seen[emoji]; dup && level != "turbo" {
			t.Errorf("levels %q and %q share the emoji %q", other, level, emoji)
		}
		seen[emoji] = level
	}
}
