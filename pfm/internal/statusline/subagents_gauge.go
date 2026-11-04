package statusline

import "fmt"

// agentContextGauge is a row's context gauge: bar, percent, tokens, window and
// capacity state. The prompt size of the agent's newest completed call, read
// from its own transcript, leads whenever it is above zero: Claude Code's
// payload tokenCount can trail the real context (behind some gateways it counts
// output only). Without one, a payload count above zero, or one the payload
// stated (a zero included), renders as it is, and with neither the tokens
// render `?`. An unknown window renders `/?`, and a capacity state the catalog
// left (missing, error) follows as `ctx {state}`.
func agentContextGauge(task *subagentTask, activity *agentActivity) string {
	tokens, known := task.TokenCount, false
	if activity != nil && activity.context > 0 {
		tokens, known = activity.context, true
	} else if task.TokenCount > 0 || task.tokenKnown {
		known = true
	}
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
