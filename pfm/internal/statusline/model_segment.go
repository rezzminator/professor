package statusline

import "strings"

// modelSegment is the main line's model block: the model's symbol and name,
// then the effort glyph and level joined by a muted "·" — the same shape the sub-agent
// rows give their model (subagentModel). No effort renders the model alone.
func modelSegment(data input) string {
	modelSymbol := "●"
	switch {
	case strings.Contains(data.Model.DisplayName, "Fable"):
		modelSymbol = "✦"
	case strings.Contains(data.Model.DisplayName, "Opus"):
		modelSymbol = "◆"
	case strings.Contains(data.Model.DisplayName, "Sonnet"):
		modelSymbol = "◇"
	case strings.Contains(data.Model.DisplayName, "Haiku"):
		modelSymbol = "○"
	}
	return cModel + modelSymbol + " " + data.Model.DisplayName + reset + effortSuffix(data)
}

// effortSuffix renders the effort level for the model block; a level whose
// thinking is off stays visible but muted, marked "(off)".
func effortSuffix(data input) string {
	if data.Effort.Level == "" {
		return ""
	}
	joint := cMuted + "·" + reset
	if !data.Thinking.Enabled {
		return joint + cMuted + effortLabel(data.Effort.Level) + " (off)" + reset
	}
	return joint + cEffort + effortLabel(data.Effort.Level) + reset
}

// effortGlyphs are the symbols Claude Code's /effort picker draws for each
// level (2.1.281), so a level reads the same here as in Claude Code.
var effortGlyphs = map[string]string{"low": "○", "medium": "◐", "high": "●", "xhigh": "◉", "max": "◈"}

// effortLabel prefixes a level with its glyph; a level Claude Code does not
// name shows bare rather than borrowing another level's symbol.
func effortLabel(level string) string {
	if glyph, ok := effortGlyphs[level]; ok {
		return glyph + " " + level
	}
	return level
}
