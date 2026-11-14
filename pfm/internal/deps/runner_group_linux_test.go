package deps

import (
	"syscall"
	"testing"
)

// TestGroupGoneReadsARefusedGroupSignal proves a refused signal to a process group means the group
// is gone only when the kernel says there is no such group: any other refusal stands.
func TestGroupGoneReadsARefusedGroupSignal(t *testing.T) {
	for _, row := range []struct {
		refusal error
		want    bool
	}{
		{syscall.ESRCH, true},
		{syscall.EPERM, false},
		{syscall.EINVAL, false},
	} {
		if got := GroupGone(syscall.Getpgrp(), row.refusal); got != row.want {
			t.Errorf("GroupGone(%v) = %v, want %v", row.refusal, got, row.want)
		}
	}
}
