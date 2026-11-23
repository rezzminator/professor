package mockengine

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/mockengine/control"
)

// errControlTaken is a control FIFO another live mock process already reads.
var errControlTaken = errors.New("another mock engine process already reads this control FIFO")

// claim takes the FIFO's reader lock for pid, writing pid into it; a lock another process holds is
// errControlTaken naming both processes, as two readers would race for each command.
func (channel *controlChannel) claim(pid int) error {
	path := control.ReaderPath(channel.fifo)
	lock, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open the control FIFO's reader lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder, readErr := os.ReadFile(path)
		_ = lock.Close()
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("lock the control FIFO's reader lock %s: %w", path, err)
		}
		if readErr != nil {
			return fmt.Errorf("%w: %s, process %d refused (its reader's pid unreadable: %v)",
				errControlTaken, channel.fifo, pid, readErr)
		}
		return fmt.Errorf("%w: %s is read by process %s, process %d refused",
			errControlTaken, channel.fifo, strings.TrimSpace(string(holder)), pid)
	}
	if err := lock.Truncate(0); err != nil {
		_ = lock.Close()
		return fmt.Errorf("clear the control FIFO's reader lock %s: %w", path, err)
	}
	if _, err := lock.WriteAt([]byte(strconv.Itoa(pid)+"\n"), 0); err != nil {
		_ = lock.Close()
		return fmt.Errorf("write the control FIFO's reader lock %s: %w", path, err)
	}
	channel.reader = lock
	return nil
}

// listen opens the control FIFO once, at the mock's first timed act. A FIFO it cannot read ends this mock,
// as its timed acts would wait on a clock no test can move: its context is cancelled and Main exits
// ExitUsage, the error naming why (for a FIFO another mock process reads, both processes).
func (proc *process) listen() {
	channel := proc.channel()
	proc.listenOnce.Do(func() {
		if channel.fifo == "" {
			return
		}
		err := channel.open(proc.pid, proc.stderr)
		if err == nil {
			return
		}
		warn(proc.stderr, "%v", err)
		proc.refused = err
		if proc.abort != nil {
			proc.abort(err)
		}
	})
}

// controlRefused is the error that ended the mock because it could not read its control FIFO; nil
// otherwise.
func (proc *process) controlRefused() error {
	proc.listenOnce.Do(func() {})
	return proc.refused
}
