package hookentry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// engineName observes Claude's /rename records as well as pfm's pending clear
// name, and the title OpenCode's retitle reports as its own title event. A
// missing newborn transcript has no title; other read errors are errors.
func (reporter *herdrReporter) engineName(ctx context.Context, payload herdrPayload) (string, error) {
	if reporter.options.engine == engine.OpenCode && reporter.options.event == herdr.EventTitle {
		return reporter.usableTitle(strings.TrimSpace(payload.Title)), nil
	}
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
	var name string
	if payload.TranscriptPath != "" {
		var err error
		name, err = transcript.FileTitle(payload.TranscriptPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	// A reserved title never reaches tmux or Herdr; pfm's clear name still does.
	return pfmchat.EngineTitle(ctx, *reporter.deps.seatPaths, payload.SessionID, reporter.usableTitle(name))
}

// usableTitle is an engine's title as a chat name: a reserved label never
// reaches tmux or Herdr, it is warned about and read as no title.
func (reporter *herdrReporter) usableTitle(title string) string {
	if err := naming.ValidateLabel(title); err != nil {
		reporter.warn("pfm internal herdr-state %s: engine title not used: %v", reporter.options.event, err)
		return ""
	}
	return title
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

// syncNativeTitle names a native seat after the title OpenCode's retitle
// reports, outside the host-wide bridge lock (no pane state is written); any
// other event that plans no state report sends Herdr nothing.
func (reporter *herdrReporter) syncNativeTitle(
	ctx context.Context,
	seat fleetdb.Seat,
	payload herdrPayload,
	unlockBridge func(),
) error {
	if reporter.options.event != herdr.EventTitle {
		return nil
	}
	unlockBridge()
	return reporter.syncNativeEngineName(ctx, seat, payload)
}
