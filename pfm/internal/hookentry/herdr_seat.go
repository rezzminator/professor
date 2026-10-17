package hookentry

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// nativeSeat routes only a matching durable native seat. Legacy environment-only
// reporters and tmux-in-Herdr keep their existing bridge path.
func (reporter *herdrReporter) nativeSeat(ctx context.Context, payload herdrPayload) (handled bool, returnErr error) {
	env := reporter.deps.getenv
	if reporter.deps.seatPaths == nil || env("TMUX") != "" || env(herdr.EnvMarker) != "1" ||
		env(herdr.EnvSocket) == "" ||
		env(herdr.EnvPane) == "" {
		return false, nil
	}
	db := fleetdb.OpenSharedStateReadOnly(ctx, *reporter.deps.seatPaths)
	defer func() {
		if err := db.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close native seat state: %w", err))
		}
	}()
	if errors.Is(db.Degraded(), fleetdb.ErrAbsent) {
		return false, nil // No durable seats exist; retain the environment-only reporter.
	}
	seat, found, err := db.SeatByPane(ctx, env(herdr.EnvSocket), env(herdr.EnvPane))
	if err != nil {
		return true, fmt.Errorf("resolve native seat: %w", err)
	}
	if !found {
		return false, nil
	}
	if seat.Engine != string(reporter.options.engine) {
		return true, fmt.Errorf(
			"native seat %s engine %s does not match reporter %s",
			seat.SeatID,
			seat.Engine,
			reporter.options.engine,
		)
	}
	// A native seat has no tmux client lifecycle, including hooks from old bridges.
	if reporter.options.event.FiredByTmux() {
		return true, nil
	}
	action := reporter.plan(payload)
	if action == nil {
		return true, nil
	}
	before := seat
	if payload.SessionID != "" {
		seat.SessionID = payload.SessionID
	}
	reporter.session = seat.SessionID
	// A bound state-only hook needs no schema work or durable heartbeat write.
	if reporter.options.event == herdr.EventStart || reporter.options.event == herdr.EventEnd ||
		seat.SessionID != before.SessionID {
		writable := fleetdb.OpenSharedState(ctx, *reporter.deps.seatPaths)
		defer func() {
			if err := writable.Close(); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close native seat binding: %w", err))
			}
		}()
		seat.UpdatedAt = reporter.deps.now().Unix()
		if err := writable.UpdateSeat(ctx, before, seat); err != nil {
			return true, fmt.Errorf("bind native seat: %w", err)
		}
	}
	reporter.nativeSeatReport = true
	action.stored.SessionID = seat.SessionID
	seq := reporter.seq
	if seq == 0 {
		seq = reporter.nextSent()
	}
	return true, reporter.send(
		ctx,
		herdrTarget{socket: seat.HerdrSocket, pane: seat.HerdrPane},
		action,
		seq,
	)
}

func (reporter *herdrReporter) resumeArgv(sessionID string) ([]string, error) {
	if !reporter.nativeSeatReport {
		return herdr.ResumeArgv(sessionID)
	}
	// Charge the fixed restored flag to the validator's byte budget. The
	// resulting five words remain below its 64-word limit; the added suffix
	// contains no forbidden characters, so all session validation is retained.
	argv, err := herdr.ResumeArgv(sessionID + "--restored")
	if err != nil {
		return nil, err
	}
	argv[len(argv)-1] = sessionID
	return append(argv[:3:3], "--restored", sessionID), nil
}

// releaseSettled turns Herdr's pane-not-found for a release (the pane closed
// since it was resolved) into success; every other failure remains visible.
func releaseSettled(err error) error {
	if isHerdrNotFound(err) {
		return nil
	}
	return err
}

func isHerdrNotFound(err error) bool {
	var refusal *herdr.Refusal
	return errors.As(err, &refusal) && (refusal.Code == herdrPaneNotFound || refusal.Code == herdrNotFound)
}

// tmuxSeatLifecycle updates only this launcher's durable row; it does not
// change tmux bridge state, hooks, target selection or attach choreography.
func (reporter *herdrReporter) tmuxSeatLifecycle(ctx context.Context, payload herdrPayload) (returnErr error) {
	if payload.SessionID == "" ||
		(reporter.options.event != herdr.EventStart && reporter.options.event != herdr.EventEnd) {
		return nil
	}
	socket, _, _ := strings.Cut(reporter.deps.getenv("TMUX"), ",")
	db := fleetdb.OpenSharedState(ctx, *reporter.deps.seatPaths)
	defer func() {
		if err := db.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close tmux seat state: %w", err))
		}
	}()
	seat, found, err := db.Seat(ctx, filepath.Base(socket))
	if err != nil {
		return fmt.Errorf("read tmux seat: %w", err)
	}
	if !found || seat.Backend != "tmux" {
		return nil
	}
	if reporter.options.event == herdr.EventEnd {
		if payload.Reason == herdrReasonClear || payload.Reason == herdrReasonResume || seat.ReloadPending {
			return nil
		}
		return db.DeleteSeat(ctx, seat.SeatID)
	}
	before := seat
	seat.SessionID = payload.SessionID
	return db.UpdateSeat(ctx, before, seat)
}
