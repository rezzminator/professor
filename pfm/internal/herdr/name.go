package herdr

import (
	"strconv"
	"strings"
)

// ChatNameOption is the tmux session option holding a chat's exact name. Every
// pfm window-name writer sets it in the same tmux command as the rename; the
// window name itself is clipped, this option is not.
const ChatNameOption = "@pfm_chat_name"

// EventName re-sends a chat's name to the Herdr panes registered for its
// session; the tmux window-renamed hook runs it.
const EventName Event = "name"

// agentNameMax is Herdr's agent-name length cap.
const agentNameMax = 32

// AgentName is the Herdr agent name for a chat name: lowercase, every run of
// characters outside [a-z0-9_-] one `-`, `-` and `_` trimmed at both ends, an
// empty result `chat`, a non-letter start prefixed `c`, capped at 32 and
// trimmed again. A name already in taken gets the shortest free suffix `-2`,
// `-3`, …, the base trimmed so the whole stays within 32.
func AgentName(name string, taken map[string]bool) string {
	var slug strings.Builder
	pendingDash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			if pendingDash {
				slug.WriteByte('-')
				pendingDash = false
			}
			slug.WriteRune(r)
			continue
		}
		pendingDash = slug.Len() > 0
	}
	base := strings.Trim(slug.String(), "-_")
	if base == "" {
		base = "chat"
	}
	if base[0] < 'a' || base[0] > 'z' {
		base = "c" + base
	}
	base = trimAgentNameEnd(base, agentNameMax)
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		candidate := trimAgentNameEnd(base, agentNameMax-len(suffix)) + suffix
		if !taken[candidate] {
			return candidate
		}
	}
}

func trimAgentNameEnd(value string, limit int) string {
	if len(value) > limit {
		value = value[:limit]
	}
	return strings.TrimRight(value, "-_")
}
