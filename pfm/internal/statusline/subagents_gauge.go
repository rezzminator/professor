package statusline

import "fmt"

// agentContextGauge is a row's context gauge: bar, percent, tokens and window. The
// tokens are the prompt size of the agent's newest completed call, read from
// its own transcript, because Claude Code's payload tokenCount does not track
// the agent's context (it stays near zero beside a million tokens of spend).
// With no completed call recorded — no transcript, an unreadable one, none yet
// finished — the payload's count stands: an unread transcript is never a zero.
func agentContextGauge(task *subagentTask, activity *agentActivity) string {
	tokens := task.TokenCount
	if activity != nil && activity.context > 0 {
		tokens = activity.context
	}
	gauge := cTokens + formatContextTokens(tokens) + reset
	if task.ContextWindowSize > 0 {
		percent := gaugePercent(float64(tokens) * 100 / float64(task.ContextWindowSize))
		gauge = makeBar(percent, subagentBarWidth) + " " +
			percentColor(percent) + fmt.Sprintf("%d%%", percent) + reset + " " +
			gauge + cWindow + "/" + formatContextTokens(task.ContextWindowSize) + reset
	}
	return gauge
}
