package mockengine

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/mockengine/control"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestControlChannelAppliesEachCommandToTheMocksClock: next steps to the armed wait and fires it,
// advance moves the clock by its duration, nudge hands the looking wait its look, and a command the
// mock cannot apply is refused by name and acked as refused, its clock unmoved.
func TestControlChannelAppliesEachCommandToTheMocksClock(t *testing.T) {
	for _, row := range []struct {
		line, ack, err string
		moved          time.Duration
	}{
		{line: "next", ack: "next 300ms", moved: 300 * time.Millisecond},
		{line: "advance 1s", ack: "advance 1s", moved: time.Second},
		{line: "advance 0s", ack: "advance 0s"},
		{line: "nudge", ack: "nudge 0s"},
		{line: "advance -1ns", err: `"advance -1ns" needs a duration of zero or more: <nil>`},
		{line: "advance soon", err: `"advance soon" needs a duration of zero or more: time: invalid duration "soon"`},
		{line: "wait", err: `unknown command "wait" (want next, advance {duration} or nudge)`},
	} {
		t.Run(row.line, func(t *testing.T) {
			channel := &controlChannel{clock: newMockClock(), beats: make(chan time.Time), done: make(chan struct{})}
			defer close(channel.done)
			start := channel.clock.Now()
			timer := channel.clock.NewTimer(300 * time.Millisecond)
			looked := make(chan time.Time, 1)
			if row.line == "nudge" {
				go func() { looked <- <-channel.beats }()
			}
			ack, err := channel.apply(row.line)
			if row.err != "" {
				if err == nil || err.Error() != row.err {
					t.Fatalf("apply(%q) error = %v, want %q", row.line, err, row.err)
				}
				channel.fifo = filepath.Join(t.TempDir(), "control")
				channel.handle(row.line, io.Discard)
				acks, err := control.Acks(channel.fifo)
				if want := []string{"refused " + row.err}; err != nil || !slices.Equal(acks, want) {
					t.Fatalf(
						"handle(%q) acked %q (%v) in %s, want %q",
						row.line,
						acks,
						err,
						control.LogPath(channel.fifo),
						want,
					)
				}
			} else if err != nil || ack != row.ack {
				t.Fatalf("apply(%q) = %q, %v, want %q", row.line, ack, err, row.ack)
			}
			if moved := channel.clock.Now().Sub(start); moved != row.moved {
				t.Fatalf("apply(%q) moved the clock %s, want %s", row.line, moved, row.moved)
			}
			if row.line == "next" {
				if _, ok := testwait.Receive(t, "the armed timer to fire on next", timer.C()); !ok {
					t.Fatal("the armed timer's channel closed")
				}
			}
			if row.line == "nudge" {
				if at, _ := testwait.Receive(t, "the looking wait to take its look", looked); !at.Equal(start) {
					t.Fatalf("the look carried %s, want the unmoved clock %s", at, start)
				}
			}
		})
	}
}

// TestControlChannelAcksTheCommandInFlightBeforeItCloses: a next that fires the mock's last wait is acked in
// the FIFO's log before close returns, so a mock that ends on that move never exits before its ack.
func TestControlChannelAcksTheCommandInFlightBeforeItCloses(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "control")
	channel := newControlChannel(fifo)
	last := channel.clock.NewTimer(300 * time.Millisecond)
	if err := channel.open(os.Getpid(), io.Discard); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() {
		<-last.C()
		closed <- channel.close()
	}()
	if err := control.SendLine(fifo, control.Next); err != nil {
		t.Fatal(err)
	}
	if err, _ := testwait.Receive(t, "the mock to close once its last wait fired", closed); err != nil {
		t.Fatalf("close the control channel: %v", err)
	}
	acks, err := control.Acks(fifo)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"next 300ms"}; !slices.Equal(acks, want) {
		t.Fatalf("acks in %s once closed = %q, want %q", control.LogPath(fifo), acks, want)
	}
}

// headless is one `claude -p` mock run as its own process, so it has its own process id.
