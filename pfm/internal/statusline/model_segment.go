package statusline

import "github.com/rezzminator/professor/pfm/internal/modelglyph"

// modelSegment is the main line's model block: the model's symbol and name,
// then the effort emoji and level joined by a muted "·" — the same shape the sub-agent
// rows give their model (subagentModel). No effort renders the model alone.
func modelSegment(data input) string {
	return cModel + modelglyph.Symbol(
		data.Model.DisplayName,
	) + " " + data.Model.DisplayName + reset + effortSuffix(
		data,
	)
}

// effortSuffix renders the effort level for the model block; a level whose
// thinking is off stays visible but muted, marked "(off)".
func effortSuffix(data input) string {
	if data.Effort.Level == "" {
		return ""
	}
	joint := cMuted + "·" + reset
	if !data.Thinking.Enabled {
		return joint + cMuted + modelglyph.EffortOff + " " + data.Effort.Level + " (off)" + reset
	}
	return joint + cEffort + modelglyph.EffortLabel(data.Effort.Level) + reset
}
