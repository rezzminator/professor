package deps

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

// darwinZombie is SZOMB and darwinExiting P_WEXIT in Darwin's sys/proc.h: a process that has exited
// and waits to be reaped, and one still exiting.
const (
	darwinZombie  = 5
	darwinExiting = 0x00002000
)

// GroupGone reports whether err, a signal to process group pgid refused, means the group holds
// nothing left to signal. Darwin answers ESRCH for a reaped group and EPERM, not success, for a group
// whose every member has exited or is exiting (P_WEXIT, as gather counts it dead) and waits unreaped;
// EPERM with a live member is a real refusal, so
// the process table decides, and a table that cannot be read leaves the refusal standing.
func GroupGone(pgid int, err error) bool {
	if errors.Is(err, syscall.ESRCH) {
		return true
	}
	if !errors.Is(err, syscall.EPERM) {
		return false
	}
	members, listErr := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
	if listErr != nil {
		return false
	}
	for i := range members {
		if members[i].Proc.P_stat != darwinZombie && members[i].Proc.P_flag&darwinExiting == 0 {
			return false
		}
	}
	return true
}
