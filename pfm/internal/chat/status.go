package chat

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// StatusRequest selects one chat and the optional answers to attach.
type StatusRequest struct {
	Target string
	// Summary summarizes the last exchange (cached by transcript offset).
	Summary bool
	// Ask answers the chat's current status from its live pane capture and
	// last exchange — never cached, since a pane changes continuously.
	Ask bool
	// Engine and Model override the configured ask runner for Summary/Ask.
	Engine pfmengine.ID
	Model  string
	// Capture overrides the tmux pane read InspectSeat falls back to when the
	// transcript cannot decide working-vs-idle or blocked. Nil is the real
	// capture.
	Capture PaneCapture
}

// InspectSeat is the one inspection door `pfm chat status`, `pfm chat ls`, MCP
// status and `pfm chat watch` share: headless.Inspect's verdict from the
// transcript and socket, then the pane evidence only the screen holds.
//
// headless.Inspect stays pure — transcript and socket only. A live chat it
// could read no turn for (OpenCode writes no transcript this process reads; a
// fresh Claude or Codex seat has not taken a turn yet) gets its state from the
// one place the evidence exists: its own pane. A live chat stuck on a tool call
// whose transcript has gone quiet reads the pane too, to tell a tool still
// running from a dialog holding it for its human (blocked).
func InspectSeat(
	ctx context.Context,
	runtime *pfmconfig.Runtime,
	target headless.Chat,
	now time.Time,
	capture PaneCapture,
) (headless.Status, error) {
	return inspectTarget(ctx, runtime, target, now, capture, nil)
}

// SeatInspector is InspectSeat for a watch: it carries every transcript it
// reads from one poll to the next (transcript.Reader), so a poll reads what
// each seat appended since the last, not its whole transcript.
type SeatInspector struct {
	runtime     *pfmconfig.Runtime
	transcripts *transcript.Reader
}

// NewSeatInspector returns a watch's inspector.
func NewSeatInspector(runtime *pfmconfig.Runtime) *SeatInspector {
	return &SeatInspector{runtime: runtime, transcripts: new(transcript.Reader)}
}

// Inspect is InspectSeat with the real pane capture.
func (inspector *SeatInspector) Inspect(
	ctx context.Context,
	target headless.Chat,
	now time.Time,
) (headless.Status, error) {
	return inspectTarget(ctx, inspector.runtime, target, now, nil, inspector.transcripts)
}

// Sweep lets go of the transcripts not inspected since the last Sweep: a
// watch sweeps once per poll, so it holds only the seats it still watches.
func (inspector *SeatInspector) Sweep() { inspector.transcripts.Sweep() }

func inspectTarget(
	ctx context.Context,
	runtime *pfmconfig.Runtime,
	target headless.Chat,
	now time.Time,
	capture PaneCapture,
	transcripts *transcript.Reader,
) (headless.Status, error) {
	status, err := headless.InspectWith(ctx, target, now, transcripts)
	if err != nil {
		return headless.Status{}, err
	}
	pane, err := needsPaneState(target, status)
	if err != nil {
		return headless.Status{}, err
	}
	switch {
	case target.Backend == spawn.BackendHerdr && target.Live && status.State == headless.StateWorking:
		status, err = statusFromHerdr(ctx, target, status)
	case pane:
		status, err = statusFromPane(ctx, target, status, capture, runtime)
	case awaitsPane(target, status):
		status, err = blockedFromPane(ctx, target, status, capture, runtime)
	}
	if err != nil {
		return headless.Status{}, err
	}
	return status, nil
}

// Status inspects the target. A dead chat is a status, not an error: the
// caller reads status.Alive(). Summary-cache warnings go to warnings.
func Status(
	ctx context.Context,
	runtime *pfmconfig.Runtime,
	request StatusRequest,
	warnings io.Writer,
) (headless.Status, error) {
	target, err := Target(ctx, request.Target, runtime)
	if err != nil {
		return headless.Status{}, err
	}
	status, err := InspectSeat(ctx, runtime, target, clock.Real.Now(), request.Capture)
	if err != nil {
		return headless.Status{}, err
	}
	if !request.Summary && !request.Ask {
		return status, nil
	}
	machine, err := pfmconfig.RuntimeOrDefault(runtime)
	if err != nil {
		return headless.Status{}, err
	}
	if request.Summary {
		database, err := store.Open(store.WithWarningWriter(warnings))
		if err != nil {
			return headless.Status{}, err
		}
		summary := headless.Summarize(ctx, target, headless.SummaryOptions{
			Config: machine.Config, Database: database,
			Engine: request.Engine, Model: request.Model,
		})
		status.Summary = summary.Text
		status.SummaryCached = summary.Cached
		if summary.Warning != nil && warnings != nil {
			fmt.Fprintf(warnings, "pfm chat status: summary cleanup warning: %v\n", summary.Warning)
		}
		if err := database.Close(); err != nil {
			return headless.Status{}, fmt.Errorf("close summary cache: %w", err)
		}
	}
	if request.Ask {
		// No cache: a pane changes continuously, so Ask pays an ask runner on
		// every call instead of consulting Summarize's exact-offset cache,
		// which would serve a confidently stale answer about a chat that has
		// moved on.
		answer := headless.Ask(ctx, target, headless.AskOptions{
			Config: machine.Config,
			Engine: request.Engine, Model: request.Model,
		})
		status.Ask = answer.Text
		if answer.Warning != nil && warnings != nil {
			fmt.Fprintf(warnings, "pfm chat status: ask cleanup warning: %v\n", answer.Warning)
		}
	}
	return status, nil
}
