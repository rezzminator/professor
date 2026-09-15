package seat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"hostops/pfm/internal/spawn"
)

// processGateHost places the process-tree gate at the only seat-local seam
// that is both after Codex's composer has held and before the actual brief is
// delivered: spawn.Run's SendLiteral call for that exact brief. It delegates
// every other tmux operation unchanged.
type processGateHost struct {
	Host
	processes ProcessTree
	jailer    ProcessJailer
	events    EventSink
	now       func() time.Time
	role      string
	prompt    string

	attempted    bool
	sessionMade  bool
	jail         ProcessGroupJail
	verification ProcessTreeVerification
	err          error
}

var _ spawn.Tmux = (*processGateHost)(nil)

type promptPaster interface {
	PasteLiteral(context.Context, string, string, string) error
}

func (host *processGateHost) NewSession(
	ctx context.Context,
	spec spawn.SessionSpec,
) error {
	if err := host.Host.NewSession(ctx, spec); err != nil {
		return err
	}
	host.sessionMade = true
	pid, err := host.PaneRootPID(ctx, spec.Socket, spec.Session)
	if err != nil {
		return host.recordEarlyFailure(
			ProcessTreeVerification{},
			fmt.Errorf("resolve pane root for process-group jail: %w", err),
		)
	}
	host.jail, err = host.jailer.Capture(ctx, pid)
	if err != nil {
		return host.recordEarlyFailure(
			ProcessTreeVerification{PaneRootResolved: true, PaneRootPID: pid},
			fmt.Errorf("capture pane process-group jail: %w", err),
		)
	}
	return nil
}

func (host *processGateHost) recordEarlyFailure(
	verification ProcessTreeVerification,
	gateErr error,
) error {
	host.verification = verification
	eventVerification := cloneProcessTreeVerification(verification)
	event := Event{
		Phase:       "seat.process-tree.verified",
		Seat:        host.role,
		At:          host.now(),
		Error:       gateErr.Error(),
		ProcessTree: &eventVerification,
	}
	if eventErr := host.events.Record(event); eventErr != nil {
		gateErr = errors.Join(gateErr, fmt.Errorf("record %s process-tree gate: %w", host.role, eventErr))
	}
	host.err = fmt.Errorf("%s process-tree gate: %w", host.role, gateErr)
	return host.err
}

func (host *processGateHost) SendLiteral(
	ctx context.Context,
	socket, target, value string,
) error {
	if value == host.prompt && !host.attempted {
		host.attempted = true
		host.verification, host.err = host.verify(ctx, socket, target)
		if host.err != nil {
			return host.err
		}
		if paster, ok := host.Host.(promptPaster); ok {
			return paster.PasteLiteral(ctx, socket, target, value)
		}
	}
	return host.Host.SendLiteral(ctx, socket, target, value)
}

func (host *processGateHost) verify(
	ctx context.Context,
	socket, target string,
) (ProcessTreeVerification, error) {
	verification := ProcessTreeVerification{}
	pid, gateErr := host.PaneRootPID(ctx, socket, target)
	if gateErr == nil {
		verification.PaneRootResolved = true
		verification.PaneRootPID = pid
		observed, inspectErr := host.processes.Inspect(ctx, pid)
		observed.PaneRootResolved = true
		observed.PaneRootPID = pid
		verification = observed
		gateErr = inspectErr
	}
	if gateErr == nil {
		if host.jail.RootPID != pid || host.jail.RootStartTicks != verification.Root.StartTicks {
			gateErr = fmt.Errorf("pane root changed after process-group capture")
		}
	}
	if gateErr == nil {
		gateErr = validateProcessTreeVerification(verification)
	}
	if gateErr == nil {
		verification.Clean = true
	}
	eventVerification := cloneProcessTreeVerification(verification)
	event := Event{
		Phase:       "seat.process-tree.verified",
		Seat:        host.role,
		At:          host.now(),
		ProcessTree: &eventVerification,
	}
	if gateErr != nil {
		event.Error = gateErr.Error()
	}
	if eventErr := host.events.Record(event); eventErr != nil {
		gateErr = errors.Join(gateErr, fmt.Errorf("record %s process-tree gate: %w", host.role, eventErr))
	}
	if gateErr != nil {
		return verification, fmt.Errorf("%s process-tree gate: %w", host.role, gateErr)
	}
	return verification, nil
}
