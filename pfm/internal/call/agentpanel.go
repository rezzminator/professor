package call

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// claudeAgentPanelHint is the agents panel's own footer, drawn only while the
// panel holds keyboard focus.
var claudeAgentPanelHint = regexp.MustCompile(`to select · Enter to view`)

// agentPanelFocused reports whether Claude's background-agents panel holds the
// keyboard: its footer hint is among the pane's last 16 non-blank rows and the cursor sits on an agent row.
// Keys typed then drive the panel, and an Enter opens the selected agent.
func agentPanelFocused(capture string) bool {
	tail := lastNonEmptyLines(capture, 16)
	if !claudeAgentPanelHint.MatchString(tail) {
		return false
	}
	for _, line := range strings.Split(tail, "\n") {
		visible := strings.TrimLeftFunc(ansi.Strip(strings.TrimSuffix(line, "\r")), unicode.IsSpace)
		if claudeAgentRow.MatchString(visible) {
			return true
		}
	}
	return false
}
