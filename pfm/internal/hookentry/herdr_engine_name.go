package hookentry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// engineName observes Claude's /rename records as well as pfm's pending clear
// name. A missing newborn transcript has no title; other read errors are errors.
func (reporter *herdrReporter) engineName(ctx context.Context, payload herdrPayload) (name string, returnErr error) {
	if reporter.options.engine != engine.Claude || reporter.deps.seatPaths == nil || payload.SessionID == "" {
		return "", nil
	}
	// PostToolUse and PostToolUseFailure both report working, often many
	// times per turn. A full title read belongs to lifecycle/prompt events,
	// never to that per-tool hot path; Stop still converges in-engine renames.
	if reporter.options.event == herdr.EventWorking &&
		payload.HookEventName != claudelaunch.HookEventUserPromptSubmit {
		return "", nil
	}
	if payload.TranscriptPath != "" {
		var err error
		name, err = transcript.FileTitle(payload.TranscriptPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if err := naming.ValidateLabel(name); err != nil {
		return "", err
	}
	state := fleetdb.OpenSharedStateReadOnly(ctx, *reporter.deps.seatPaths)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	if errors.Is(state.Degraded(), fleetdb.ErrAbsent) {
		return name, nil
	}
	decision, found, err := state.ClearName(ctx, payload.SessionID)
	if err != nil {
		return "", err
	}
	if found {
		if observed := decision.ObserveTitle(name); observed != decision {
			writable := fleetdb.OpenSharedState(ctx, *reporter.deps.seatPaths)
			err := writable.PutClearName(ctx, payload.SessionID, observed, clock.Real.Now().Unix())
			if err = errors.Join(err, writable.Close()); err != nil {
				return "", fmt.Errorf("confirm engine title: %w", err)
			}
			decision = observed
		}
		name = decision.Title(name)
	}
	return name, nil
}

func (reporter *herdrReporter) syncTmuxEngineName(ctx context.Context, payload herdrPayload) error {
	if !reporter.firedByEngine() || reporter.options.event == herdr.EventEnd {
		return nil
	}
	name, err := reporter.engineName(ctx, payload)
	if err != nil || name == "" {
		return err
	}
	current, err := reporter.deps.tmux(ctx, reporter.options.socket,
		"show-options", "-qv", "-t", reporter.tmuxPane, herdr.ChatNameOption)
	if err != nil {
		return fmt.Errorf("read tmux name before engine title sync: %w", err)
	}
	if strings.TrimSpace(current) == name {
		return nil
	}
	_, err = reporter.deps.tmux(ctx, reporter.options.socket, gather.ChatRenameCommand(reporter.tmuxPane, name)...)
	if err != nil {
		return fmt.Errorf("sync engine title %q to tmux: %w", name, err)
	}
	return nil
}

func (reporter *herdrReporter) syncNativeEngineName(
	ctx context.Context,
	seat fleetdb.Seat,
	payload herdrPayload,
) (returnErr error) {
	name, err := reporter.engineName(ctx, payload)
	if err != nil || name == "" || name == seat.Name {
		return err
	}
	state := fleetdb.OpenSharedState(ctx, *reporter.deps.seatPaths)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	return pfmchat.RenameHerdrOwned(ctx, state, seat, name)
}
