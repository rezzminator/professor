package ui

import (
	"strings"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/modelglyph"
)

// deckModelW is the model column: the family's symbol and the effort's emoji,
// one cell and two. The effort comes from modelglyph.RowEffort, whose emoji are
// single code points that are two cells wide in every terminal — the status
// line's variation-selector emoji would desync the columns after them.
const deckModelW = 3

// modelCell paints the model column of a row, exactly deckModelW cells. A model
// or an effort the picker could not read leaves its cells blank: it never
// guesses one.
func modelCell(row compose.Row, bg string) []span {
	palette := configuredPalette
	blank := func(cells int) span { return span{text: strings.Repeat(" ", cells), paint: tone{bg: bg}} }
	if row.Model == "" {
		return []span{blank(deckModelW)}
	}
	spans := []span{{text: modelglyph.ListSymbol(row.Model), paint: tone{fg: palette.Muted, bg: bg}}}
	if emoji := modelglyph.RowEffort(row.Effort); emoji != "" {
		return append(spans, span{text: emoji, paint: tone{bg: bg}})
	}
	return append(spans, blank(deckModelW-1))
}

// shortModel is a model id without its vendor prefix, for a pane that has room
// for the name but not for "claude-".
func shortModel(id string) string {
	return strings.TrimPrefix(id, "claude-")
}
