package mockengine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/mockengine/control"
)

// controlChannel is the scenario's control FIFO as the mock reads it: each line a command applied to its
// clock (internal/mockengine/control), acked in the FIFO's log once applied. A scenario that names no FIFO
// still has the clock, which then stands: nothing moves it.
type controlChannel struct {
	clock *mockClock
	// beats hands a look to the wait that is looking (busy, a gated agent): each command offers one, a nudge
	// waits until one is taken.
	beats chan time.Time
	file  *os.File
	// reader holds the lock that makes this process the FIFO's one reader (control.ReaderPath).
	reader *os.File
	fifo   string
	done   chan struct{}
	// acking is held while a command is applied and its ack written; close takes it, so the mock never exits
	// between moving its clock (which may fire its last wait) and acking the move.
	acking sync.Mutex
}

func newControlChannel(fifo string) *controlChannel {
	return &controlChannel{clock: newMockClock(), beats: make(chan time.Time), fifo: fifo, done: make(chan struct{})}
}

// open claims the FIFO for pid, creates it when it is missing and holds it open for reading and writing, so a
// test's write never meets a FIFO without a reader while the mock lives and the reader never sees an end.
func (channel *controlChannel) open(pid int, stderr io.Writer) error {
	if err := channel.claim(pid); err != nil {
		return err
	}
	if err := control.Make(channel.fifo); err != nil {
		return err
	}
	file, err := os.OpenFile(channel.fifo, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open the control FIFO %s: %w", channel.fifo, err)
	}
	channel.file = file
	go channel.read(stderr)
	return nil
}

func (channel *controlChannel) read(stderr io.Writer) {
	lines := bufio.NewScanner(channel.file)
	for lines.Scan() {
		if !channel.handle(lines.Text(), stderr) {
			return
		}
	}
	select {
	case <-channel.done:
	default:
		warn(stderr, "control %s: read: %v", channel.fifo, lines.Err())
	}
}

var errControlClosed = errors.New("the mock closed its control FIFO")

// handle applies one command line and acks it in the FIFO's log, holding acking throughout; false once the
// mock closed its FIFO.
func (channel *controlChannel) handle(line string, stderr io.Writer) bool {
	channel.acking.Lock()
	defer channel.acking.Unlock()
	ack, err := channel.apply(line)
	if errors.Is(err, errControlClosed) {
		return false
	}
	if err != nil {
		warn(stderr, "control %s: %v", channel.fifo, err)
		// The refusal is acked too, so a test's wait for the ack ends on it rather than at its hung limit.
		ack = control.Refused + " " + strings.Join(strings.Fields(err.Error()), " ")
	}
	if err := appendLine(control.LogPath(channel.fifo), []byte(ack)); err != nil {
		warn(stderr, "control %s: ack %q: %v", channel.fifo, ack, err)
	}
	return true
}

// apply runs one command and returns its ack line, "{command} {moved}".
func (channel *controlChannel) apply(line string) (string, error) {
	word, argument, _ := strings.Cut(strings.TrimSpace(line), " ")
	var moved time.Duration
	switch word {
	case control.Next:
		due, ok := channel.clock.next(channel.done)
		if !ok {
			return "", errControlClosed
		}
		moved = due
		channel.offer()
	case control.Advance:
		by, err := time.ParseDuration(argument)
		if err != nil || by < 0 {
			return "", fmt.Errorf("%q needs a duration of zero or more: %v", line, err)
		}
		channel.clock.Advance(by)
		moved = by
		channel.offer()
	case control.Nudge:
		select {
		case channel.beats <- channel.clock.Now():
		case <-channel.done:
			return "", errControlClosed
		}
	default:
		return "", fmt.Errorf("unknown command %q (want %s, %s {duration} or %s)",
			line, control.Next, control.Advance, control.Nudge)
	}
	return word + " " + moved.String(), nil
}

// offer hands a look to a wait looking right now, so a moved clock repaints; none looking, it is dropped.
func (channel *controlChannel) offer() {
	select {
	case channel.beats <- channel.clock.Now():
	default:
	}
}

func (channel *controlChannel) close() error {
	close(channel.done)
	// A command in flight ends its wait on done, then writes its ack before the FIFO closes.
	channel.acking.Lock()
	defer channel.acking.Unlock()
	var errs []error
	if channel.file != nil {
		errs = append(errs, channel.file.Close())
	}
	if channel.reader != nil {
		errs = append(errs, channel.reader.Close())
	}
	return errors.Join(errs...)
}

// clock is the clock every timed act of the mock runs on: a clock only its test moves, through the FIFO
// Scenario.Control names, which the first wait armed on it opens. With no FIFO named it stands.
func (proc *process) clock() clock.Clock {
	return proc.channel().clock
}

func (proc *process) channel() *controlChannel {
	proc.controlOnce.Do(func() {
		fifo := ""
		if proc.script != nil {
			fifo = control.ForPID(proc.script.Control, proc.pid)
		}
		proc.control = newControlChannel(fifo)
		proc.control.clock.onArm = proc.listen
	})
	return proc.control
}

// closeControl ends the control reader of a mock that is returning; no later act opens it again.
func (proc *process) closeControl() {
	proc.controlOnce.Do(func() {})
	proc.listenOnce.Do(func() {})
	if proc.control == nil {
		return
	}
	if err := proc.control.close(); err != nil {
		warn(proc.stderr, "close the control FIFO %s: %v", proc.control.fifo, err)
	}
}

// wait holds a timed act for d on the mock's clock; false when ctx ended first.
func (proc *process) wait(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	return proc.clock().Sleep(ctx, d) == nil
}

// lookAgain fires when a looking wait (a busy footer, a gate file) should look again: at the next control
// command, never by itself.
func (proc *process) lookAgain() <-chan time.Time {
	proc.listen()
	return proc.channel().beats
}
