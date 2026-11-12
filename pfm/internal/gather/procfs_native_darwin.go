//go:build darwin

package gather

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// nativeProcFS returns the kernel's own process table. macOS has no /proc at
// all, so this is sysctl — see procfs_darwin.go for what that can and cannot
// answer.
func nativeProcFS() ProcFS { return NewDarwinProcFS() }

// nativeHostMemory reads hw.memsize, vm_stat and vm.swapusage.
func nativeHostMemory() (Memory, error) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return Memory{}, fmt.Errorf("read Darwin host memory size: %w", err)
	}
	vmStat, err := runVMStat()
	if err != nil {
		return Memory{}, err
	}
	swap, swapErr := unix.SysctlRaw("vm.swapusage")
	return darwinMemory(total, vmStat, swap, swapErr)
}

// nativeHostCPU: macOS has no host tick counter reachable without cgo.
func nativeHostCPU() (CPU, error) {
	return CPU{}, fmt.Errorf("read host CPU counters: %w", errors.ErrUnsupported)
}

// nativeHostCPUPressure: macOS has no pressure stall information.
func nativeHostCPUPressure() (float64, error) {
	return 0, fmt.Errorf("read CPU pressure: %w", errors.ErrUnsupported)
}

// nativeProcessCounters takes one fresh ps snapshot.
func nativeProcessCounters() (map[int]ProcessCounter, []string, error) {
	output, err := runPSSnapshot()
	if err != nil {
		return nil, nil, fmt.Errorf("sample Darwin process counters: %w", err)
	}
	rows, warnings := parsePSSnapshot(output)
	counters := make(map[int]ProcessCounter, len(rows))
	for pid, row := range rows {
		counters[pid] = row.counter
	}
	return counters, warnings, nil
}

// nativeUnixSockets: macOS publishes no socket table.
func nativeUnixSockets() ([]string, error) {
	return nil, fmt.Errorf("list unix sockets: %w", errors.ErrUnsupported)
}

// Darwin's sys/proc.h: SZOMB is a process that has exited and waits to be reaped, P_WEXIT one
// still working on its exit (ps prints it E), which the kernel no longer reads arguments from.
const (
	darwinZombieState = 5
	darwinExitingFlag = 0x00002000
)

// darwinDead reports a process that can do no more work: a zombie, or one already exiting.
func darwinDead(state int8, flags int32) bool {
	return state == darwinZombieState || flags&darwinExitingFlag != 0
}
