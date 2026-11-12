package mockengine

import (
	"context"
	"errors"
)

// finish ends a Claude session: its SessionEnd hooks, then the seat release.
func (session *claudeSession) finish(reason string) error {
	payload := session.payload(hookSessionEnd)
	payload.Reason = reason
	_, err := session.fire(hookSessionEnd, "", payload)
	if session.seat != nil {
		err = errors.Join(err, session.seat.release())
		session.seat = nil
	}
	return err
}

// hookContext is the context an engine's hook of event runs under. A hangup or
// terminate signal (tmux kill-server, a closed Herdr pane) has already
// cancelled the process context when SessionEnd fires, and a real engine
// still runs its SessionEnd hooks to completion before it exits: they run
// detached from that cancel, each handler bounded by its own timeout
// (runHookCommand).
func hookContext(ctx context.Context, event string) context.Context {
	if event == hookSessionEnd {
		return context.WithoutCancel(ctx)
	}
	return ctx
}
