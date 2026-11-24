// Package controltest is how a test makes the mock engine act: it moves the mock's clock and hands
// its waits a look through the scenario's control FIFO (internal/mockengine/control), and releases a
// fixture script blocked on a FIFO of its own. Every call returns once the mock (or the script) took
// it, waiting through internal/testwait; none sleeps or bounds by a duration of its own.
//
// Entry points: Next, AdvanceBy, Nudge, FIFO, Release. No other files.
package controltest

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/mockengine/control"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// Next moves the mock's clock to its earliest armed wait and returns how far it moved. Send it once
// the wait is armed (the busy footer shows, the hook started): a ticker armed earlier is otherwise
// the one it reaches.
func Next(t testing.TB, fifo string) time.Duration {
	t.Helper()
	moved, err := time.ParseDuration(sendCommand(t, fifo, control.Next))
	if err != nil {
		t.Fatalf("the ack of %s on %s: %v", control.Next, fifo, err)
	}
	return moved
}

// AdvanceBy moves the mock's clock by d: a busy footer's elapsed counter goes with it, and every wait
// due by then fires.
func AdvanceBy(t testing.TB, fifo string, d time.Duration) {
	t.Helper()
	sendCommand(t, fifo, control.Advance+" "+d.String())
}

// Nudge hands the looking wait one look and returns once it took it: the look before it is done, so
// what the pane shows now is what that look found.
func Nudge(t testing.TB, fifo string) {
	t.Helper()
	sendCommand(t, fifo, control.Nudge)
}

// FIFO creates a FIFO at path for a fixture script to block on (`read -r _ < "$fifo"`) and returns
// path; Release lets the script go on.
func FIFO(t testing.TB, path string) string {
	t.Helper()
	if err := control.Make(path); err != nil {
		t.Fatal(err)
	}
	return path
}

// Release waits until a reader holds fifo open, then writes it one line: the script blocked reading it
// goes on. The wait is the presence that the script reached its block.
func Release(t testing.TB, fifo string) {
	t.Helper()
	deliver(t, fifo, "go", "a reader to block on "+fifo)
}

// sendCommand sends one control line and returns the moved duration of its ack.
func sendCommand(t testing.TB, fifo, line string) string {
	t.Helper()
	before, err := control.Acks(fifo)
	if err != nil {
		t.Fatal(err)
	}
	deliver(t, fifo, line, "a mock engine to read its control FIFO "+fifo)
	var ack string
	testwait.Until(t, "the mock engine to ack "+line+" in "+control.LogPath(fifo), func() (bool, string) {
		acks, err := control.Acks(fifo)
		if err != nil {
			return false, err.Error()
		}
		if len(acks) <= len(before) {
			return false, fmt.Sprintf("acks %q", acks)
		}
		ack = acks[len(before)]
		return true, ""
	})
	word, moved, _ := strings.Cut(ack, " ")
	if want, _, _ := strings.Cut(line, " "); word != want {
		t.Fatalf("the mock acked %q for %q in %s", ack, line, control.LogPath(fifo))
	}
	return moved
}

func deliver(t testing.TB, fifo, line, condition string) {
	t.Helper()
	testwait.Until(t, condition, func() (bool, string) {
		err := control.SendLine(fifo, line)
		if err == nil {
			return true, ""
		}
		if !errors.Is(err, control.ErrNoReader) {
			t.Fatal(err)
		}
		return false, err.Error()
	})
}
