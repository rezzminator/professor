package statusline

import "strings"

// modelSegment is the main line's model block: the model's symbol and name,
// then the effort level joined by a muted "·" — the same shape the sub-agent
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
		return joint + cMuted + data.Effort.Level + " (off)" + reset
	}
	return joint + cEffort + data.Effort.Level + reset
}
