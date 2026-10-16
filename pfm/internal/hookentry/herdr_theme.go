package hookentry

import (
	"context"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// themeAgentState is the agent state the theme's colours show for one event:
// the state the Herdr plan reports, except a finished turn (Stop) is "done" —
// a state of its own the tmux theme fades to idle on the idle prompt. "" means
// the event changes no state (a compaction restart, a session end, a tmux-fired
// verb).
func (reporter *herdrReporter) themeAgentState(payload herdrPayload) string {
	action := reporter.plan(payload)
	if action == nil || action.release {
		return ""
	}
	if reporter.options.event == herdr.EventIdle {
		return "done"
	}
	return action.stored.State
}

// themeState marks the chat's own tmux server with its agent state while the
// `pfm theme` sync is on. It is fail-open like the rest of the reporter: a
// refusal is logged and the Herdr report goes on.
func (reporter *herdrReporter) themeState(ctx context.Context, payload herdrPayload) {
	state := reporter.themeAgentState(payload)
	if state == "" {
		return
	}
	if _, err := reporter.deps.tmux(ctx, reporter.options.socket, themesync.TmuxStateOption(state)...); err != nil {
		reporter.warn(
			"pfm internal herdr-state %s: tmux theme state %s: %v (fail-open)",
			reporter.options.event,
			state,
			err,
		)
	}
}
