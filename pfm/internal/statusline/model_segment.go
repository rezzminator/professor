package statusline

import "strings"

// modelSegment is the main line's model block: the model's symbol and name,
// then the effort emoji and level joined by a muted "·" — the same shape the sub-agent
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
		return joint + cMuted + effortEmojiOff + " " + data.Effort.Level + " (off)" + reset
	}
	return joint + cEffort + effortLabel(data.Effort.Level) + reset
}

// effortEmoji marks each effort level; a level not listed here — one Claude
// Code adds later — wears effortEmojiOther, and a level whose thinking is off
// wears effortEmojiOff instead of its own.
var effortEmoji = map[string]string{
	"low": "🚲", "medium": "🏍️", "high": "🏎️", "xhigh": "🚀", "max": "🛰️",
}

const (
	effortEmojiOther = "🔆"
	effortEmojiOff   = "💤"
)

// effortLabel prefixes a level with its emoji.
func effortLabel(level string) string {
	if emoji, ok := effortEmoji[level]; ok {
		return emoji + " " + level
	}
	return effortEmojiOther + " " + level
}
