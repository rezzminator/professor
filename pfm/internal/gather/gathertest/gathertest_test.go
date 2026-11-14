package gathertest_test

import (
	"errors"
	"io/fs"
	"slices"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/gather/gathertest"
)

// TestTableReadsAsAProcessTable: the door's callers trust the fake to answer
// as the kernel's table does — a missing pid is gone, a withheld executable
// is a permission error, and every other read returns its row.
func TestTableReadsAsAProcessTable(t *testing.T) {
	t.Parallel()
	table := &gathertest.Table{Processes: map[int]*gathertest.Process{
		301: {Start: 1301, UID: 4100, Argv: []string{"pfm", "chat", "watch"}, Exe: "/opt/pfm"},
		302: {Start: 1302, UID: 4100, Argv: []string{"claude"}},
	}}
	identity, err := gather.IdentifyProcess(table, 301)
	if err != nil {
		t.Fatalf("IdentifyProcess(301) error = %v", err)
	}
	want := gather.Identity{
		PID:        301,
		Start:      1301,
		UID:        4100,
		Argv:       []string{"pfm", "chat", "watch"},
		Executable: "/opt/pfm",
	}
	if !slices.Equal(identity.Argv, want.Argv) || identity.PID != want.PID || identity.Start != want.Start ||
		identity.UID != want.UID || identity.Executable != want.Executable {
		t.Fatalf("IdentifyProcess(301) = %+v, want %+v", identity, want)
	}
	if _, err := table.Executable(302); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Executable(302) error = %v, want %v", err, fs.ErrPermission)
	}
	if _, err := table.Stat(303); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat(303) error = %v, want %v", err, fs.ErrNotExist)
	}
}

// TestKernelRecordsOnlyDeliveredSignals: a refused open or a failed delivery
// sends nothing, and ExitOn ends a process only on the signals it names.
func TestKernelRecordsOnlyDeliveredSignals(t *testing.T) {
	t.Parallel()
	table := &gathertest.Table{Processes: map[int]*gathertest.Process{
		301: {Start: 1301}, 302: {Start: 1302},
	}}
	exit := gathertest.ExitOn(table, syscall.SIGKILL)
	kernel := &gathertest.Kernel{
		OnOpen: func(pid int) error {
			if pid == 303 {
				return syscall.ESRCH
			}
			return nil
		},
		OnSignal: func(pid int, sig syscall.Signal) error {
			if pid == 302 {
				return syscall.EPERM
			}
			return exit(pid, sig)
		},
	}
	if _, err := kernel.Open(303); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("Open(303) error = %v, want %v", err, syscall.ESRCH)
	}
	for _, sent := range []gathertest.Sent{
		{PID: 301, Sig: syscall.SIGTERM}, {PID: 302, Sig: syscall.SIGKILL}, {PID: 301, Sig: syscall.SIGKILL},
	} {
		handle, err := kernel.Open(sent.PID)
		if err != nil {
			t.Fatalf("Open(%d) error = %v", sent.PID, err)
		}
		signalErr := handle.Signal(sent.Sig)
		if wantErr := sent.PID == 302; (signalErr != nil) != wantErr {
			t.Fatalf("Signal(%d, %v) error = %v, want failure %v", sent.PID, sent.Sig, signalErr, wantErr)
		}
	}
	wantSent := []gathertest.Sent{{PID: 301, Sig: syscall.SIGTERM}, {PID: 301, Sig: syscall.SIGKILL}}
	if !slices.Equal(kernel.Sent, wantSent) || kernel.Opens != 4 {
		t.Fatalf("sent = %v opens = %d, want %v opens 4", kernel.Sent, kernel.Opens, wantSent)
	}
	if !table.Processes[301].Zombie || table.Processes[302].Zombie {
		t.Fatalf("zombies 301=%v 302=%v, want 301 only", table.Processes[301].Zombie, table.Processes[302].Zombie)
	}
}
