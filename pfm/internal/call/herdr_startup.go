package call

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// awaitHerdrPane waits only for a durable reservation's address to be published.
// It never substitutes a name, session, or another pane for the reserved seat.
func (engine *Engine) awaitHerdrPane(ctx context.Context, target Target) (result Target, returnErr error) {
	values, err := paths.Resolve()
	if err != nil {
		return target, fmt.Errorf("resolve startup seat paths: %w", err)
	}
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	bound := engine.herdrStartupBound()
	deadline := engine.options.Clock.Now().Add(bound)
	for {
		seat, found, err := db.Seat(ctx, target.SeatID)
		if err != nil {
			return target, fmt.Errorf("read startup seat %s: %w", target.SeatID, err)
		}
		if !found || seat.Backend != backendHerdr || seat.State != "live" || seat.EndPending ||
			seat.HerdrSocket != target.SocketPath || seat.Engine != target.Engine {
			return target, fmt.Errorf("startup seat %s is gone or its durable owner changed", target.SeatID)
		}
		if seat.SeatHostPID > 0 {
			dead, err := gather.HerdrHostDead(gather.NewProcFS(values.ProcRoot), seat)
			if err != nil {
				return target, fmt.Errorf("read startup seat %s supervisor: %w", target.SeatID, err)
			}
			if dead {
				return target, fmt.Errorf("startup seat %s supervisor is gone", target.SeatID)
			}
		}
		if seat.HerdrPane != "" {
			target.Pane, target.HerdrWorkspace, target.ID = seat.HerdrPane, seat.HerdrWorkspace, seat.SessionID
			return target, nil
		}
		if !engine.options.Clock.Now().Before(deadline) {
			return target, fmt.Errorf("seat %s did not publish its pane within %s", target.SeatID, bound)
		}
		if err := engine.options.Clock.Sleep(
			ctx,
			min(engine.options.Poll, deadline.Sub(engine.options.Clock.Now())),
		); err != nil {
			return target, fmt.Errorf("wait for seat %s pane publication: %w", target.SeatID, err)
		}
	}
}

// awaitHerdrNative waits before any input for the native agent and composer.
// Only positive startup observations are retried; failed reads and ownership
// proofs return their cause, while blocked UIs and drafts remain refusals.
func (engine *Engine) awaitHerdrNative(
	ctx context.Context, target Target, base *Result, lock *targetLock,
) (Target, herdrAgentResult, string, error) {
	bound := engine.herdrStartupBound()
	deadline := engine.options.Clock.Now().Add(bound)
	for {
		proved, err := ProveHerdrTarget(ctx, target)
		if err != nil {
			return target, herdrAgentResult{}, "", &herdr.InputNotSentError{
				Cause: fmt.Errorf("prove native startup owner: %w", err),
			}
		}
		target = proved
		result, err := herdr.SeatRequest(ctx, target.SocketPath, "agent.get", map[string]string{"target": target.Pane})
		if err != nil {
			return target, herdrAgentResult{}, "", fmt.Errorf(
				"herdr read target %q on %q: %w",
				target.Pane,
				target.SocketPath,
				err,
			)
		}
		agent, err := decodeHerdrAgent(result, "agent_info", target)
		if err != nil {
			base.Code, base.Message = CodeUndelivered, err.Error()
			return target, agent, "", nil
		}
		screen := ""
		if agent.Agent.Status != herdrUnknown {
			if agent.Agent.Status == herdrWorking || agent.Agent.Status == herdrBlocked {
				return target, agent, screen, nil
			}
			screen, _, err = herdrCaptureFormat(ctx, target, herdrRecentLines, "")
			if err != nil {
				return target, agent, screen, fmt.Errorf("read native startup composer: %w", err)
			}
			if refused, held := refuseTrustDialog(*base, target.Pane, screen); held {
				*base = refused
				return target, agent, screen, nil
			}
			if ComposerRowShown(screen) || SelectorLine(screen) != "" {
				return target, agent, screen, nil
			}
		}
		if !engine.options.Clock.Now().Before(deadline) {
			return target, agent, screen, fmt.Errorf(
				"%w (native agent %s within %s)",
				ErrStartupSplash,
				agent.Agent.Status,
				bound,
			)
		}
		if err := lock.beat(); err != nil {
			return target, agent, screen, fmt.Errorf("hold native startup lock: %w", err)
		}
		if err := engine.options.Clock.Sleep(
			ctx,
			min(engine.options.Poll, deadline.Sub(engine.options.Clock.Now())),
		); err != nil {
			return target, agent, screen, fmt.Errorf("wait for native startup: %w", err)
		}
	}
}

func (engine *Engine) herdrStartupBound() time.Duration {
	if engine.options.ComposerWait > 0 {
		return engine.options.ComposerWait
	}
	return CodexComposerWait
}

func (engine *Engine) proveHerdrCallTarget(ctx context.Context, target Target) (Target, error) {
	if target.Pane == "" && target.SeatID != "" {
		published, err := engine.awaitHerdrPane(ctx, target)
		if err != nil {
			return target, fmt.Errorf("herdr startup could not publish its pane: %w", err)
		}
		target = published
	}
	return ProveHerdrTarget(ctx, target)
}
