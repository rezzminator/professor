package hookentry

import (
	"context"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// themeOn reports whether the reporter marks its tmux server with the agent
// state: only a pfm chat server carries the theme, and only while its sync is on.
func (reporter *herdrReporter) themeOn() bool {
	return gather.IsChatSocketName(filepath.Base(reporter.options.socket)) && reporter.themeSyncOn()
}

// themeSyncOn reports whether the `pfm theme` sync is on, whatever server
// the reporter runs in.
func (reporter *herdrReporter) themeSyncOn() bool {
	return reporter.deps.stateDir != "" && themesync.SyncOn(reporter.deps.stateDir)
}

// themeAgentState is the agent state the theme's colours show for one event:
// the state the Herdr plan reports, except a finished turn (Stop) is "done" —
// a state of its own the tmux theme fades to idle on the idle prompt — and a
// session end clears the state, so a server that outlives its engine keeps no
// working, blocked or done colour. A nil vector means the event changes no
// state (a compaction restart, a tmux-fired verb).
func (reporter *herdrReporter) themeAgentState(payload herdrPayload) (state string, option []string) {
	action := reporter.plan(payload)
	switch {
	case action == nil:
		return "", nil
	case action.release:
		return "cleared", themesync.TmuxStateClear()
	case reporter.options.event == herdr.EventIdle:
		return "done", themesync.TmuxStateOption("done")
	}
	return action.stored.State, themesync.TmuxStateOption(action.stored.State)
}

// themeState marks the chat's own tmux server with its agent state while the
// `pfm theme` sync is on. It is fail-open like the rest of the reporter: a
// refusal is logged and the Herdr report goes on.
func (reporter *herdrReporter) themeState(ctx context.Context, payload herdrPayload) {
	state, option := reporter.themeAgentState(payload)
	if option == nil {
		return
	}
	if _, err := reporter.deps.tmux(ctx, reporter.options.socket, option...); err != nil {
		reporter.warn(
			"pfm internal herdr-state %s: tmux theme state %s: %v (fail-open)",
			reporter.options.event,
			state,
			err,
		)
	}
}
