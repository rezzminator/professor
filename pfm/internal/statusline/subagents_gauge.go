package statusline

import "fmt"

// agentContextGauge is a row's context gauge: bar, percent, tokens, window and
// capacity state. The tokens are the prompt size of the agent's newest
// completed call, read from its own transcript; before one lands, and right
// after a compaction, they render `?`. An unknown window renders `/?`, and a
// capacity state the catalog left (missing, error) follows as `ctx {state}`.
func agentContextGauge(task *subagentTask, activity *agentActivity) string {
	tokens, known := activity.context, activity.context > 0
	gauge := cTokens + "?" + reset
	if known {
		gauge = cTokens + formatContextTokens(tokens) + reset
	}
	if task.ContextWindowSize > 0 {
		if known {
			percent := gaugePercent(float64(tokens) * 100 / float64(task.ContextWindowSize))
			gauge = makeBar(percent, subagentBarWidth) + " " +
				percentColor(percent) + fmt.Sprintf("%d%%", percent) + reset + " " + gauge
		}
		gauge += cWindow + "/" + formatContextTokens(task.ContextWindowSize) + reset
	} else {
		gauge += cWindow + "/?" + reset
	}
	if task.capacityState != "" {
		gauge += " " + cWarn + "ctx " + task.capacityState + reset
	}
	return gauge
}
