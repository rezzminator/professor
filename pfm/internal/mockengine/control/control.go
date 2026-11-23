// Package control is the wire between a test and the mock engine's clock: the FIFO a scenario's
// control field names, the commands a test writes on it one line each, and the log the mock acks
// each applied command in. The mock (internal/mockengine) reads the FIFO; a test writes it through
// internal/mockengine/control/controltest. Neither side ever waits on a clock of its own: the mock
// blocks reading the FIFO, and the test's wait for the ack is its hang guard.
//
// Entry points: the command words, PIDToken, ForPID, Make, SendLine, LogPath, ReaderPath and Acks. No other
// files.
package control

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// PIDToken in a scenario's control path stands for the process id of the mock engine reading it, so one
// scenario shared by many mock processes gives each its own FIFO.
const PIDToken = "{pid}"

// ForPID is the control path the mock engine with process id pid reads: path with every PIDToken replaced.
func ForPID(path string, pid int) string {
	return strings.ReplaceAll(path, PIDToken, strconv.Itoa(pid))
}

// The commands, one per line on the FIFO. Each is acked in the log as "{command} {moved}", moved the
// duration the mock's clock went forward ("0s" for a nudge).
const (
	// Next moves the mock's clock to its earliest armed wait (a busy turn's end, a ticker, a hook's
	// timeout), first waiting until one is armed.
	Next = "next"
	// Advance moves the mock's clock by the Go duration after it: "advance 1s".
	Advance = "advance"
	// Nudge hands one look to the wait that is looking: a busy turn repaints its footer and stats its
	// gate, a gated background agent stats its gate. It waits until such a wait takes it.
	Nudge = "nudge"
	// Refused is the ack of a command the mock could not apply, "refused {why}": an unknown word, a
	// malformed duration.
	Refused = "refused"
)

// ErrNoReader is a SendLine that found no mock holding the FIFO open: none started yet, or it exited.
var ErrNoReader = errors.New("no mock engine reads the control FIFO")

// Make creates the FIFO at path, keeping one that already exists.
func Make(path string) error {
	if err := syscall.Mkfifo(path, 0o600); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create the control FIFO %s: %w", path, err)
	}
	return nil
}

// SendLine writes one line to the FIFO without blocking: ErrNoReader when no process has it open for
// reading. A mock engine holds its control FIFO open from its first timed act; a fixture script
// blocked in `read -r _ < fifo` holds it while it waits.
func SendLine(fifo, line string) error {
	file, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENXIO) {
		return fmt.Errorf("%w: %s", ErrNoReader, fifo)
	}
	if err != nil {
		return fmt.Errorf("open the control FIFO %s: %w", fifo, err)
	}
	_, writeErr := file.WriteString(line + "\n")
	if closeErr := file.Close(); closeErr != nil {
		writeErr = errors.Join(writeErr, closeErr)
	}
	if writeErr != nil {
		return fmt.Errorf("write %q to the control FIFO %s: %w", line, fifo, writeErr)
	}
	return nil
}

// LogPath is the file the mock appends one ack line to for each command it applied from fifo.
func LogPath(fifo string) string {
	return fifo + ".log"
}

// ReaderPath is the lock file the one mock engine reading fifo holds, its process id written in it: a second
// mock process that finds it held exits naming both.
func ReaderPath(fifo string) string {
	return fifo + ".reader"
}

// Acks is every ack line the mock has written for fifo so far; a log not written yet holds none.
func Acks(fifo string) ([]string, error) {
	content, err := os.ReadFile(LogPath(fifo))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the control log of %s: %w", fifo, err)
	}
	text := strings.TrimSuffix(string(content), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}
