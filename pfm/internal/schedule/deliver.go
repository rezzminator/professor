package schedule

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/inject"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

const (
	scheduleFireCommand = "pfm internal schedule-fire"
	// scheduleComposerReads is how many consecutive captures must show the
	// composer before a revived chat is typed into: one read can catch a
	// splash frame that paints the glyph before the input is live.
	scheduleComposerReads = 3
	// scheduleCaptureLines is the scrollback a composer read looks at.
	scheduleCaptureLines = 40
	scheduleOpened       = "opened"
)

// ComposerWait bounds the revived composer wait; the installer gate bounds its wait by it.
const ComposerWait = 90 * time.Second

// Package variables so a test shortens the wait.
var (
	scheduleComposerPoll = 500 * time.Millisecond
	scheduleComposerWait = ComposerWait
)

// chatDeliverer wakes a chat and types a schedule into it: a live chat
// is injected as it stands, a dead one is resumed detached on a fresh tmux
// socket and typed into once its composer is up. The seams default to the real
// doors; a test replaces them.
type chatDeliverer struct {
	runtime *pfmconfig.Runtime
	stderr  io.Writer
	tmuxDir string
	open    func(ctx context.Context, sessionID string) (action.OpenResult, error)
	capture func(ctx context.Context, socketPath, target string) (string, error)
	inject  func(ctx context.Context, r fleetdb.Schedule, target, message string) error
}

func newChatDeliverer(runtime *pfmconfig.Runtime, stderr io.Writer) *chatDeliverer {
	deliverer := &chatDeliverer{runtime: runtime, stderr: stderr, tmuxDir: runtime.Paths.TmuxDir}
	deliverer.open = func(ctx context.Context, sessionID string) (action.OpenResult, error) {
		return pfmchat.OpenDetachedID(ctx, sessionID, stderr, runtime)
	}
	deliverer.capture = func(ctx context.Context, socketPath, target string) (string, error) {
		return (inject.TmuxInjector{}).Capture(ctx, socketPath, target, false, scheduleCaptureLines)
	}
	deliverer.inject = deliverer.injectSchedule
	return deliverer
}

// Deliver implements Deliverer.
func (deliverer *chatDeliverer) Deliver(ctx context.Context, r fleetdb.Schedule, message string) error {
	result, err := deliverer.open(ctx, r.SessionID)
	if err != nil {
		return fmt.Errorf("resolve chat %s (%s): %w", r.SessionID, r.Label, err)
	}
	target := r.SessionID
	if result.State == scheduleOpened {
		if err := deliverer.awaitComposer(ctx, result.Socket); err != nil {
			return err
		}
		target = result.Socket
	}
	return deliverer.inject(ctx, r, target, message)
}

// awaitComposer polls a revived chat's pane until its composer shows on
// consecutive reads. A folder-trust dialog fails at once: its one key is the
// human's decision, never a schedule's.
func (deliverer *chatDeliverer) awaitComposer(ctx context.Context, socket string) error {
	socketPath := filepath.Join(deliverer.tmuxDir, socket)
	deadline := clock.Real.Now().Add(scheduleComposerWait)
	streak := 0
	var lastErr error
	for {
		capture, err := deliverer.capture(ctx, socketPath, socket)
		switch {
		case err != nil:
			streak, lastErr = 0, err
		case pfmengine.ClaudeTrustDialog(capture):
			return fmt.Errorf("revived chat on %s is held at the folder-trust dialog; nothing typed", socket)
		case inject.LastComposerLine(capture) != "":
			streak++
			if streak >= scheduleComposerReads {
				return nil
			}
		default:
			streak = 0
		}
		if !clock.Real.Now().Before(deadline) {
			failure := fmt.Errorf(
				"revived chat on socket %s showed no composer within %s; "+
					"it stays running and the next tick will inject it",
				socket, scheduleComposerWait,
			)
			if lastErr != nil {
				failure = fmt.Errorf("%w (last capture error: %v)", failure, lastErr)
			}
			return failure
		}
		if err := clock.Real.Sleep(ctx, scheduleComposerPoll); err != nil {
			return fmt.Errorf("wait for composer on socket %s: %w", socket, err)
		}
	}
}

// injectSchedule types the message the way its setter would have: the
// schedule is a deferred message from that chat, signed as the setter's.
func (deliverer *chatDeliverer) injectSchedule(
	ctx context.Context,
	r fleetdb.Schedule,
	target, message string,
) error {
	engine, err := pfmchat.NewInjectEngine(true, deliverer.runtime)
	if err != nil {
		return fmt.Errorf("build injector for %s: %w", target, err)
	}
	if r.SetByID != "" {
		engine = engine.WithIdentity(resolve.Identity{ID: r.SetByID}, r.SetByLabel)
	}
	res, err := engine.Inject(ctx, inject.Request{Target: target, Message: message})
	if err != nil {
		return fmt.Errorf("inject into %s: %w", target, err)
	}
	if res.Code != 0 || !res.Typed {
		return fmt.Errorf("inject into %s: code %d: %s", target, res.Code, res.Message)
	}
	return nil
}

// RunFire is `pfm internal schedule-fire`: the scheduler's tick.
func RunFire(args []string, stdout, stderr io.Writer, runtime *pfmconfig.Runtime) int {
	if len(args) != 0 {
		fmt.Fprintf(stderr, "usage: %s\n", scheduleFireCommand)
		return 2
	}
	effective, err := pfmconfig.RuntimeOrDefault(runtime)
	if err != nil {
		fmt.Fprintf(stderr, "%s: read runtime: %v\n", scheduleFireCommand, err)
		return 1
	}
	ctx := context.Background()
	state := fleetdb.OpenSharedState(ctx, effective.Paths)
	if err := state.Degraded(); err != nil {
		fmt.Fprintf(stderr, "%s: open shared state: %v\n", scheduleFireCommand, err)
		if closeErr := state.Close(); closeErr != nil {
			fmt.Fprintf(stderr, "%s: close shared state: %v\n", scheduleFireCommand, closeErr)
		}
		return 1
	}
	report, fireErr := Fire(
		ctx, state, newChatDeliverer(&effective, stderr), FireLockPath(effective.Paths), clock.Real.Now(),
	)
	code := 0
	if closeErr := state.Close(); closeErr != nil {
		fmt.Fprintf(stderr, "%s: close shared state: %v\n", scheduleFireCommand, closeErr)
		code = 1
	}
	if fireErr != nil {
		fmt.Fprintf(stderr, "%s: %v\n", scheduleFireCommand, fireErr)
		return 1
	}
	if report.Skipped {
		fmt.Fprintf(stderr, "%s: another fire holds the lock; skipped\n", scheduleFireCommand)
		return code
	}
	for index := range report.Fired {
		fired := &report.Fired[index]
		fmt.Fprintf(stdout, "fired schedule %d → %s\n", fired.ID, scheduleChatColumnFull(fired))
	}
	for index := range report.Failed {
		failure := &report.Failed[index]
		fmt.Fprintf(stderr, "%s: schedule %d (%s, %s): %v — stays due\n",
			scheduleFireCommand, failure.Schedule.ID, failure.Schedule.Label, failure.Schedule.SessionID, failure.Err)
		code = 1
	}
	return code
}

// scheduleChatColumnFull names a schedule's chat for the fire log: its label,
// else its whole session id.
func scheduleChatColumnFull(r *fleetdb.Schedule) string {
	if r.Label != "" {
		return r.Label
	}
	return r.SessionID
}
