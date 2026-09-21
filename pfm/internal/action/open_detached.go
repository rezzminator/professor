package action

import (
	"context"
	"errors"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// OpenResult is what OpenDetached hands back to a caller with no terminal:
// the chat's display name, the socket it now lives on, and whether it was
// already live or was just started.
type OpenResult struct {
	Name   string
	Socket string
	State  string
	// Detail is one sentence about how the chat was opened that the caller
	// could not otherwise see — today, that a resumable row whose recorded
	// directory has disappeared was opened somewhere else instead. It is
	// written by whoever prepared the Request, because that is who knows the
	// substitution was made; empty means the open held no surprise. A caller
	// with no terminal never looks at the chat it started, so a difference
	// like that has to ride back in the result rather than in a log line.
	Detail string
}

// OpenDetached opens the target row for a caller that has no terminal — the
// MCP daemon, which must survive the call rather than be replaced by it. It
// never dispatches a K1 eval line and never calls execute/syscall.Exec: a
// live chat already has a running session, so nothing is spawned and its own
// socket is reported ("live"); a resumable chat is started on its own fresh
// tmux server through the package's existing spawn door
// (TmuxClient.CreateChatServer, backed by spawn.TmuxSpawner.NewSession — the
// same door Executor.Open uses once Synthesize has built its ChatServer), and
// the socket it now lives on is reported ("opened").
func (executor *Executor) OpenDetached(
	ctx context.Context,
	request Request,
) (result OpenResult, err error) {
	// The same trail Executor.Open keeps: a chat opened over MCP has as much
	// of a birth as one opened from a terminal, and without this its only
	// trace in the activity log would be whatever Solo happened to record.
	trail := obs.NewTrail(ctx, "action", "requested")
	defer func() { trail.End(err) }()
	if executor == nil {
		return OpenResult{}, errors.New("action executor is nil")
	}
	if request.Row.Kind.IsLiveSeat() {
		if !executor.tmux.SocketAlive(ctx, request.Row.Socket) {
			// Mirrors Open()'s own dead-socket fallback: a live row whose
			// socket has disappeared resumes fresh rather than being reported
			// live over a socket nothing answers on.
			if request.Row.ID == "" {
				return OpenResult{}, fmt.Errorf(
					"open detached: live socket %q died and its split row has no resumable id",
					request.Row.Socket,
				)
			}
			request.Row.Kind = compose.ResumeKindFor(request.Row.Kind)
			request.Row.Socket = ""
			request.Row.SessionName = ""
			request.Row.WindowName = ""
		}
	}

	switch request.Row.Kind {
	case compose.Agent, compose.ResumeClaude:
		if err := executor.Solo(
			ctx,
			request.Row.ID,
			"",
			request.Row.Kind == compose.Agent,
			request.Config.Claude.Binary,
		); err != nil {
			return OpenResult{}, fmt.Errorf("open detached: %w", err)
		}
	case compose.ResumeCodex:
		if executor.heal != nil {
			if message := executor.heal(ctx, request.Row.ID); message != "" {
				fmt.Fprintln(executor.stderr, message)
			}
		}
	}

	plan, err := Synthesize(request)
	if err != nil {
		return OpenResult{}, fmt.Errorf("open detached: %w", err)
	}
	if plan.ChatServer == nil {
		// Live and booting rows already have a running session — there is
		// nothing to spawn, and the eval line Synthesize built is an attach
		// instruction for a terminal this caller does not have.
		trail.Reach("opened", "already live")
		return OpenResult{Name: request.Row.Name, Socket: request.Row.Socket, State: "live"}, nil
	}
	if err := executor.tmux.CreateChatServer(ctx, *plan.ChatServer); err != nil {
		return OpenResult{}, fmt.Errorf("open detached: %w", err)
	}
	trail.Reach("opened", "detached server created")
	return OpenResult{Name: request.Row.Name, Socket: plan.ChatServer.Socket, State: "opened"}, nil
}
