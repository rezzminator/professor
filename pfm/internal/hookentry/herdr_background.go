package hookentry

import (
	"fmt"
	"strings"
)

// herdrBackgroundTask is one entry of the background_tasks a Claude Stop hook
// carries: work the session registered that is still in flight, running or
// pending in the background. Only its type is read, in Claude Code's own
// words (Claude Code 2.1.331, binary probe: `background_tasks:` beside
// `hook_event_name:"Stop"`, its labels in the map holding `local_bash:"shell"`).
type herdrBackgroundTask struct {
	Type string `json:"type"`
}

// herdrBackgroundAgent is the type of a background sub-agent's task.
const herdrBackgroundAgent = "subagent"

// herdrBackgroundKinds are the task types that keep a stopped chat working,
// in the order its message names them, each with its noun: the ones whose end
// Claude Code hands the chat as a task notification, which starts its next
// turn. Any other type (a dream, an auto-mode scan) ends without waking the
// chat, so nothing would take its count back.
var herdrBackgroundKinds = []struct{ kind, noun string }{
	{herdrBackgroundAgent, "agent"},
	{"shell", "shell"},
	{"monitor", "monitor"},
	{"workflow", "workflow"},
	{"MCP task", "MCP task"},
}

// herdrBackgroundCounts counts a Stop payload's background tasks by type, the
// herdrBackgroundKinds alone. nil when it names none: an empty list, a Claude
// Code older than the field, the transcript watch's idle.
func herdrBackgroundCounts(tasks []herdrBackgroundTask) map[string]int {
	var counts map[string]int
	for _, task := range tasks {
		for _, each := range herdrBackgroundKinds {
			if task.Type != each.kind {
				continue
			}
			if counts == nil {
				counts = map[string]int{}
			}
			counts[each.kind]++
		}
	}
	return counts
}

// herdrInFlight reports whether a stored state still holds work that outlives
// the main turn: a sub-agent whose turn is open, or background work its last
// Stop listed.
func herdrInFlight(stored herdrStored) bool {
	return len(stored.Agents) > 0 || len(stored.Background) > 0
}

// herdrInFlightMessage names that work for the pane, "1 agent, 2 shells
// running", "" when there is none. Agents are the larger of the two counts:
// the open turns the lifecycle hooks counted (a nested agent's too), and the
// background agents the Stop listed (one waiting on its own shell has no open
// turn).
func herdrInFlightMessage(stored herdrStored) string {
	var parts []string
	for _, each := range herdrBackgroundKinds {
		count := stored.Background[each.kind]
		if each.kind == herdrBackgroundAgent {
			count = max(count, len(stored.Agents))
		}
		if count == 0 {
			continue
		}
		noun := each.noun
		if count != 1 {
			noun += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s", count, noun))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ") + " running"
}
