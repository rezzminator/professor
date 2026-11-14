// Package gathertest is the in-memory process table and kernel the signal
// door's callers test against: a gather.ProcFS that reads owners, start times
// and executables, and a gather.Kernel that records every signal it delivers.
package gathertest

import (
	"context"
	"io/fs"
	"maps"
	"slices"
	"sort"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

// Process is one process generation in a Table. An empty Exe reads as an
// executable the table withholds (permission denied).
type Process struct {
	Start      uint64
	UID        uint32
	ParentPID  int
	Command    string
	Argv       []string
	Exe        string
	Zombie     bool
	Environ    map[string]string
	EnvironErr error
}

// Table is a process table keyed by pid; a pid missing from it is gone.
type Table struct {
	Processes map[int]*Process
}

func (table *Table) lookup(pid int) (*Process, error) {
	process, found := table.Processes[pid]
	if !found {
		return nil, fs.ErrNotExist
	}
	return process, nil
}

// Identity is the identity the door reads for pid, for a test to capture as
// discovery would.
func (table *Table) Identity(pid int) gather.Identity {
	process := table.Processes[pid]
	return gather.Identity{
		PID: pid, Start: process.Start, UID: process.UID, Argv: slices.Clone(process.Argv), Executable: process.Exe,
	}
}

// PIDs lists the table's pids in ascending order.
func (table *Table) PIDs() ([]int, error) {
	pids := make([]int, 0, len(table.Processes))
	for pid := range table.Processes {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids, nil
}

// Cmdline is pid's argv.
func (table *Table) Cmdline(pid int) ([]string, error) {
	process, err := table.lookup(pid)
	if err != nil {
		return nil, err
	}
	return slices.Clone(process.Argv), nil
}

// Environ is pid's environment, or its EnvironErr.
func (table *Table) Environ(pid int) (map[string]string, error) {
	process, err := table.lookup(pid)
	if err != nil {
		return nil, err
	}
	if process.EnvironErr != nil {
		return nil, process.EnvironErr
	}
	return maps.Clone(process.Environ), nil
}

// FDLinks reads no descriptors.
func (table *Table) FDLinks(int) ([]gather.FDLink, error) { return nil, nil }

// Stat is pid's parent and start time.
func (table *Table) Stat(pid int) (gather.ProcStat, error) {
	process, err := table.lookup(pid)
	if err != nil {
		return gather.ProcStat{}, err
	}
	return gather.ProcStat{ParentPID: process.ParentPID, StartTime: process.Start}, nil
}

// ProcessIdentity is pid's owner, command name and zombie state.
func (table *Table) ProcessIdentity(pid int) (gather.ProcessIdentity, error) {
	process, err := table.lookup(pid)
	if err != nil {
		return gather.ProcessIdentity{}, err
	}
	return gather.ProcessIdentity{EffectiveUID: process.UID, Zombie: process.Zombie, Command: process.Command}, nil
}

// Executable is pid's executable path; an empty Exe is withheld.
func (table *Table) Executable(pid int) (string, error) {
	process, err := table.lookup(pid)
	if err != nil {
		return "", err
	}
	if process.Exe == "" {
		return "", fs.ErrPermission
	}
	return process.Exe, nil
}

// Sent is one signal the Kernel delivered.
type Sent struct {
	PID int
	Sig syscall.Signal
}

// Kernel opens and signals pids for the door. OnOpen runs as a pid is opened,
// before the door re-reads it — the gap a reused pid slips into; an error
// refuses the open (syscall.ESRCH: no such process). OnSignal is how the
// process answers a signal; an error fails the delivery, which is then not
// recorded. Nil hooks open every pid and leave every process running.
type Kernel struct {
	OnOpen   func(pid int) error
	OnSignal func(pid int, sig syscall.Signal) error
	Opens    int
	Sent     []Sent
}

// Open opens pid unless OnOpen refuses it.
func (kernel *Kernel) Open(pid int) (gather.Handle, error) {
	kernel.Opens++
	if kernel.OnOpen != nil {
		if err := kernel.OnOpen(pid); err != nil {
			return nil, err
		}
	}
	return handle{kernel: kernel, pid: pid}, nil
}

type handle struct {
	kernel *Kernel
	pid    int
}

func (handle handle) Signal(sig syscall.Signal) error {
	if handle.kernel.OnSignal != nil {
		if err := handle.kernel.OnSignal(handle.pid, sig); err != nil {
			return err
		}
	}
	handle.kernel.Sent = append(handle.kernel.Sent, Sent{PID: handle.pid, Sig: sig})
	return nil
}

func (handle) Close() error { return nil }

// ExitOn is an OnSignal for table: a process receiving one of sigs exits
// (becomes a zombie), any other signal leaves it running.
func ExitOn(table *Table, sigs ...syscall.Signal) func(pid int, sig syscall.Signal) error {
	return func(pid int, sig syscall.Signal) error {
		if process, found := table.Processes[pid]; found && slices.Contains(sigs, sig) {
			process.Zombie = true
		}
		return nil
	}
}

// StepClock advances at once on Sleep, so a grace runs to its deadline without
// blocking the test.
type StepClock struct{ *clock.Fake }

// Sleep advances the fake clock by d unless ctx is done.
func (clk StepClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clk.Advance(d)
	return nil
}

// Door is a door over procs and kernel on a StepClock, acting as uid from the
// process self.
func Door(procs gather.ProcFS, kernel *Kernel, uid uint32, self int) gather.Door {
	return gather.Door{
		Procs:  procs,
		Kernel: kernel,
		Clock:  StepClock{clock.NewFake(time.Unix(1_700_000_000, 0))},
		UID:    uid,
		Self:   self,
	}
}
