// Package modelglyph owns the glyphs that name a model and the effort it runs
// at: the one table the status line and the picker's rows both read, so a model
// never wears two symbols and an effort level never wears two emoji.
//
// The status line writes each effort as a free-standing emoji, some of them a
// base character plus a variation selector (🏍️ 🏎️ 🛰️). A terminal that counts
// such a pair as one cell while the program counts two throws every cell after
// it off by a column, which a status line survives and a column-aligned list
// does not. RowEffort is therefore the list's own form: every glyph in it is a
// single code point that is wide by default, so its width is two cells in every
// terminal that draws emoji at all.
package modelglyph

import "strings"

const (
	// EffortOther marks an effort level this table does not know — one Claude
	// Code adds later — and EffortOff one whose thinking is off.
	EffortOther = "🔆"
	EffortOff   = "💤"
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

// statusEffort is the status line's emoji per effort level.
var statusEffort = map[string]string{
	"low": "🚲", "medium": "🏍️", "high": "🏎️", "xhigh": "🚀", "max": "🛰️",
}

// rowEffort is the list's width-safe emoji per level, slowest to fastest.
var rowEffort = map[string]string{
	"low": "🚲", "medium": "🛵", "high": "🚄", "xhigh": "🚀", "max": "🛸",
}

// EffortEmoji is the status line's emoji for a level: EffortOther for a level
// not in the table, and "" for no level at all.
func EffortEmoji(level string) string {
	if level == "" {
		return ""
	}
	if emoji, ok := statusEffort[level]; ok {
		return emoji
	}
	return EffortOther
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

// EffortLabel prefixes a level with its status-line emoji.
func EffortLabel(level string) string {
	return EffortEmoji(level) + " " + level
}
