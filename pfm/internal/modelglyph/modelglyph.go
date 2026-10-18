// Package modelglyph owns model symbols, statusline effort bars and width-safe picker glyphs.
package modelglyph

import "strings"

const (
	// EffortOther marks an effort level this table does not know — one Claude
	// Code adds later — in the picker.
	EffortOther = "🔆"
	// Unknown is the symbol of a model whose family this table does not know.
	Unknown = "●"
)

// families are the model families the symbol distinguishes, matched as a
// lower-case substring of a model's display name or id.
var families = []struct{ match, symbol string }{
	{"fable", "✦"},
	{"opus", "◆"},
	{"sonnet", "◇"},
	{"haiku", "○"},
}

// Symbol is the one-cell symbol of a model, from its display name ("Opus 4") or
// its id ("claude-opus-4-7") alike; a model of no known family wears Unknown.
func Symbol(model string) string {
	lower := strings.ToLower(model)
	for _, family := range families {
		if strings.Contains(lower, family.match) {
			return family.symbol
		}
	}
	return Unknown
}

// ListSymbol is Symbol for a column of a list: a model of no known family wears
// a diamond rather than Unknown's dot, which a list already spends on "live".
func ListSymbol(model string) string {
	if symbol := Symbol(model); symbol != Unknown {
		return symbol
	}
	return "◈"
}

// statusEffort is the statusline's bar per effort level: the filled steps of
// a five-step scale, no empty ones.
var statusEffort = map[string]string{
	"low": "▰", "medium": "▰▰", "high": "▰▰▰", "xhigh": "▰▰▰▰", "max": "▰▰▰▰▰",
}

// rowEffort is the list's width-safe emoji per level, slowest to fastest.
var rowEffort = map[string]string{
	"low": "🚲", "medium": "🛵", "high": "🚄", "xhigh": "🚀", "max": "🛸",
}

// RowEffort is the list's emoji for a level: always two cells wide, EffortOther
// for a level not in the table, and "" for no level at all.
func RowEffort(level string) string {
	if level == "" {
		return ""
	}
	if emoji, ok := rowEffort[level]; ok {
		return emoji
	}
	return EffortOther
}

// EffortBar normalises a level to lowercase and returns its filled-only bar:
// medium → ("medium", "▰▰"). A level outside the five has no bar; no level
// returns two empty strings.
func EffortBar(level string) (name, bar string) {
	name = strings.ToLower(strings.TrimSpace(level))
	return name, statusEffort[name]
}

// EffortLabel renders a lowercase level after its filled-only bar, medium →
// "▰▰ medium"; a level outside the five stays explicit, without a bar.
func EffortLabel(level string) string {
	name, bar := EffortBar(level)
	switch {
	case name == "":
		return ""
	case bar == "":
		return "effort " + name + " (unknown)"
	default:
		return bar + " " + name
	}
}
