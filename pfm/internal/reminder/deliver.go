package reminder

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
	reminderFireCommand = "pfm internal reminder-fire"
	// reminderComposerReads is how many consecutive captures must show the
	// composer before a revived chat is typed into: one read can catch a
	// splash frame that paints the glyph before the input is live.
	reminderComposerReads = 3
	// reminderCaptureLines is the scrollback a composer read looks at.
	reminderCaptureLines = 40
	reminderOpened       = "opened"
)

// Package variables so a test shortens the wait.
var (
	reminderComposerPoll = 500 * time.Millisecond
	reminderComposerWait = 90 * time.Second
)

// chatDeliverer wakes a chat and types a reminder into it: a live chat
// is injected as it stands, a dead one is resumed detached on a fresh tmux
// socket and typed into once its composer is up. The seams default to the real
// doors; a test replaces them.
type chatDeliverer struct {
	runtime *pfmconfig.Runtime
	stderr  io.Writer
	tmuxDir string
	open    func(ctx context.Context, sessionID string) (action.OpenResult, error)
	capture func(ctx context.Context, socketPath, target string) (string, error)
	inject  func(ctx context.Context, r fleetdb.Reminder, target, message string) error
}

func newChatDeliverer(runtime *pfmconfig.Runtime, stderr io.Writer) *chatDeliverer {
	deliverer := &chatDeliverer{runtime: runtime, stderr: stderr, tmuxDir: runtime.Paths.TmuxDir}
	deliverer.open = func(ctx context.Context, sessionID string) (action.OpenResult, error) {
		return pfmchat.OpenDetachedID(ctx, sessionID, stderr, runtime)
	}
	deliverer.capture = func(ctx context.Context, socketPath, target string) (string, error) {
		return (inject.TmuxInjector{}).Capture(ctx, socketPath, target, false, reminderCaptureLines)
	}
	deliverer.inject = deliverer.injectReminder
	return deliverer
}

// Deliver implements Deliverer.
func (deliverer *chatDeliverer) Deliver(ctx context.Context, r fleetdb.Reminder, message string) error {
	result, err := deliverer.open(ctx, r.SessionID)
	if err != nil {
		return fmt.Errorf("resolve chat %s (%s): %w", r.SessionID, r.Label, err)
	}
	target := r.SessionID
	if result.State == reminderOpened {
		if err := deliverer.awaitComposer(ctx, result.Socket); err != nil {
			return err
		}
		target = result.Socket
	}
	return deliverer.inject(ctx, r, target, message)
}

// awaitComposer polls a revived chat's pane until its composer shows on
// consecutive reads. A folder-trust dialog fails at once: its one key is the
// human's decision, never a reminder's.
func (deliverer *chatDeliverer) awaitComposer(ctx context.Context, socket string) error {
	socketPath := filepath.Join(deliverer.tmuxDir, socket)
	deadline := clock.Real.Now().Add(reminderComposerWait)
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
			if streak >= reminderComposerReads {
				return nil
			}
		default:
			streak = 0
		}
		if !clock.Real.Now().Before(deadline) {
			failure := fmt.Errorf(
				"revived chat on socket %s showed no composer within %s; "+
					"it stays running and the next tick will inject it",
				socket, reminderComposerWait,
			)
			if lastErr != nil {
				failure = fmt.Errorf("%w (last capture error: %v)", failure, lastErr)
			}
			return failure
		}
		if err := clock.Real.Sleep(ctx, reminderComposerPoll); err != nil {
			return fmt.Errorf("wait for composer on socket %s: %w", socket, err)
		}
	}
}

// injectReminder types the message the way its setter would have: the
// reminder is a deferred message from that chat, signed as the setter's.
func (deliverer *chatDeliverer) injectReminder(
	ctx context.Context,
	r fleetdb.Reminder,
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

// RunFire is `pfm internal reminder-fire`: the scheduler's tick.
func RunFire(args []string, stdout, stderr io.Writer, runtime *pfmconfig.Runtime) int {
	if len(args) != 0 {
		fmt.Fprintf(stderr, "usage: %s\n", reminderFireCommand)
		return 2
	}
	effective, err := pfmconfig.RuntimeOrDefault(runtime)
	if err != nil {
		fmt.Fprintf(stderr, "%s: read runtime: %v\n", reminderFireCommand, err)
		return 1
	}
	ctx := context.Background()
	state := fleetdb.OpenSharedState(ctx, effective.Paths)
	if err := state.Degraded(); err != nil {
		fmt.Fprintf(stderr, "%s: open shared state: %v\n", reminderFireCommand, err)
		if closeErr := state.Close(); closeErr != nil {
			fmt.Fprintf(stderr, "%s: close shared state: %v\n", reminderFireCommand, closeErr)
		}
		return 1
	}
	report, fireErr := Fire(
		ctx, state, newChatDeliverer(&effective, stderr), FireLockPath(effective.Paths), clock.Real.Now(),
	)
	code := 0
	if closeErr := state.Close(); closeErr != nil {
		fmt.Fprintf(stderr, "%s: close shared state: %v\n", reminderFireCommand, closeErr)
		code = 1
	}
	if fireErr != nil {
		fmt.Fprintf(stderr, "%s: %v\n", reminderFireCommand, fireErr)
		return 1
	}
	if report.Skipped {
		fmt.Fprintf(stderr, "%s: another fire holds the lock; skipped\n", reminderFireCommand)
		return code
	}
	for index := range report.Fired {
		fired := &report.Fired[index]
		fmt.Fprintf(stdout, "fired reminder %d → %s\n", fired.ID, reminderChatColumnFull(fired))
	}
	for index := range report.Failed {
		failure := &report.Failed[index]
		fmt.Fprintf(stderr, "%s: reminder %d (%s, %s): %v — stays due\n",
			reminderFireCommand, failure.Reminder.ID, failure.Reminder.Label, failure.Reminder.SessionID, failure.Err)
		code = 1
	}
	return code
}

// reminderChatColumnFull names a reminder's chat for the fire log: its label,
// else its whole session id.
func reminderChatColumnFull(r *fleetdb.Reminder) string {
	if r.Label != "" {
		return r.Label
	}
	return r.SessionID
}
