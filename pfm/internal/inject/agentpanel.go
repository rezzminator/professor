package inject

import (
	"regexp"
	"strings"
	"unicode"
)

// claudeAgentPanelHint is the agents panel's own footer, drawn only while the
// panel holds keyboard focus.
var claudeAgentPanelHint = regexp.MustCompile(`to select · Enter to view`)

// agentPanelFocused reports whether Claude's background-agents panel holds the
// keyboard: its footer hint is on screen and the cursor sits on an agent row.
// Keys typed then drive the panel, and an Enter opens the selected agent.
func agentPanelFocused(capture string) bool {
	tail := captureLastLines(capture, 16)
	if !claudeAgentPanelHint.MatchString(tail) {
		return false
	}
	for _, line := range strings.Split(tail, "\n") {
		visible := strings.TrimLeftFunc(stripTerminalControl(strings.TrimSuffix(line, "\r")), unicode.IsSpace)
		if claudeAgentRow.MatchString(visible) {
			return true
		}
	}
	return false
}
