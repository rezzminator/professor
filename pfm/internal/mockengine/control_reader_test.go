package mockengine

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/mockengine/control"
)

// listening is one mock run, in process, that has opened its control FIFO: its stderr and the cause its
// abort received.
type listening struct {
	proc    *process
	stderr  *bytes.Buffer
	aborted error
}

// listen starts the control reader of a mock run as pid whose scenario names path, as its first timed
// act does; the reader is closed when the test ends.
func listen(t *testing.T, path string, pid int) *listening {
	t.Helper()
	run := &listening{stderr: &bytes.Buffer{}}
	run.proc = &process{pid: pid, stderr: run.stderr, script: &script{Scenario: Scenario{Control: path}}}
	run.proc.abort = func(cause error) { run.aborted = cause }
	run.proc.listen()
	t.Cleanup(run.proc.closeControl)
	return run
}

// readerLock is the pid a FIFO's reader lock names.
func readerLock(t *testing.T, fifo string) string {
	t.Helper()
	holder, err := os.ReadFile(control.ReaderPath(fifo))
	if err != nil {
		t.Fatalf("read the reader lock of %s: %v", fifo, err)
	}
	return string(holder)
}

// TestControlFIFOHasOneReaderPerProcess: a control path with {pid} gives each mock process its own FIFO, so
// two live at once both read; a path two processes share is read by the first, and the second is refused
// naming both; a path the mock cannot open refuses it naming why, never a hang. A refused mock's abort
// carries the refusal, which Main turns into ExitUsage.
func TestControlFIFOHasOneReaderPerProcess(t *testing.T) {
	t.Run("a path with the pid token", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "c-"+control.PIDToken)
		for _, pid := range []int{101, 102} {
			run := listen(t, path, pid)
			fifo := filepath.Join(filepath.Dir(path), fmt.Sprintf("c-%d", pid))
			if err := run.proc.controlRefused(); err != nil || run.aborted != nil || run.stderr.String() != "" {
				t.Fatalf("mock %d refused %v, aborted %v, stderr %q; want it reading %s",
					pid, err, run.aborted, run.stderr.String(), fifo)
			}
			if got, want := readerLock(t, fifo), fmt.Sprintf("%d\n", pid); got != want {
				t.Fatalf("reader lock of %s = %q, want %q", fifo, got, want)
			}
		}
	})
	t.Run("a path two processes share", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "control")
		first := listen(t, path, 101)
		second := listen(t, path, 102)
		if err := first.proc.controlRefused(); err != nil {
			t.Fatalf("first mock refused: %v", err)
		}
		refused := second.proc.controlRefused()
		if !errors.Is(refused, errControlTaken) || second.aborted != refused {
			t.Fatalf("second mock refused %v, aborted %v; want errControlTaken as both", refused, second.aborted)
		}
		want := fmt.Sprintf("mock-engine: another mock engine process already reads this control FIFO: "+
			"%s is read by process 101, process 102 refused\n", path)
		if got := second.stderr.String(); got != want {
			t.Fatalf("second mock stderr = %q, want %q", got, want)
		}
		if got := readerLock(t, path); got != "101\n" {
			t.Fatalf("reader lock of %s = %q, want the first mock's %q", path, got, "101\n")
		}
	})
	t.Run("a path the mock cannot open", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing", "control")
		run := listen(t, path, 101)
		refused := run.proc.controlRefused()
		if refused == nil || run.aborted != refused {
			t.Fatalf("mock refused %v, aborted %v; want the open error as both", refused, run.aborted)
		}
		lock := control.ReaderPath(path)
		want := fmt.Sprintf("mock-engine: open the control FIFO's reader lock %s: open %s: no such file or directory\n",
			lock, lock)
		if got := run.stderr.String(); got != want {
			t.Fatalf("mock stderr = %q, want %q", got, want)
		}
	})
}
