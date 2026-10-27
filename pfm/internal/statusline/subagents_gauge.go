package statusline

import "fmt"

// agentContextGauge is a row's context gauge: bar, percent, tokens, window and
// capacity state. Claude Code's payload tokenCount is taken only when it is
// above zero and shows a percent above 0% (with no window there is no percent,
// so any count above zero stands): a missing count, a stated zero, or a count
// too small to register against the window often trails the agent's real
// context. Otherwise the prompt size of the agent's newest completed call, read
// from its own transcript, stands in. Without one, a count the payload stated
// (a zero included) renders as it is, and with none the tokens render `?`. An
// unknown window renders `/?`, and a capacity state the catalog left (missing,
// error) follows as `ctx {state}`.
func agentContextGauge(task *subagentTask, activity *agentActivity) string {
	tokens := task.TokenCount
	known := tokens > 0 && (task.ContextWindowSize <= 0 ||
		gaugePercent(float64(tokens)*100/float64(task.ContextWindowSize)) > 0)
	if !known && activity != nil && activity.context > 0 {
		tokens, known = activity.context, true
	}
	if !known && (task.TokenCount > 0 || task.tokenKnown) {
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
