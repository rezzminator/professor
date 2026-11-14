//go:build jail && darwin

package deps

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"
)

// TestKillGroupOfAnExitedUnreapedGroupSucceeds proves KillGroup reads Darwin's refusal of a group
// whose only member has exited and waits unreaped as a group already gone: the kernel answers EPERM
// there, and a caller that kills the group before it waits (every deferred teardown) must not fail.
func TestKillGroupOfAnExitedUnreapedGroupSucceeds(t *testing.T) {
	process, err := (RealRunner{}).Start(
		context.Background(),
		[]string{"sh", "-c", "exit 0"},
		StartOptions{ProcessGroup: true},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = process.Wait() })
	deadline := time.Now().Add(10 * time.Second)
	for {
		refusal := syscall.Kill(-process.Pid(), 0)
		if errors.Is(refusal, syscall.EPERM) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("signal 0 to exited, unreaped group %d = %v after 10s, want EPERM", process.Pid(), refusal)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := process.KillGroup(); err != nil {
		t.Fatalf("KillGroup() of exited, unreaped group %d = %v, want nil", process.Pid(), err)
	}
}

// TestGroupGoneReadsARefusedSignalToALiveGroup proves a refused signal to a group with a live member
// means the group is gone only when the kernel says there is no such group: EPERM there is a real
// refusal, never the exited group's.
func TestGroupGoneReadsARefusedSignalToALiveGroup(t *testing.T) {
	process, err := (RealRunner{}).Start(
		context.Background(),
		[]string{"sh", "-c", "sleep 30"},
		StartOptions{ProcessGroup: true},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _, _ = process.KillGroup(), process.Wait() })
	for _, row := range []struct {
		refusal error
		want    bool
	}{
		{syscall.ESRCH, true},
		{syscall.EPERM, false},
		{syscall.EINVAL, false},
	} {
		if got := GroupGone(process.Pid(), row.refusal); got != row.want {
			t.Errorf("GroupGone(live group %d, %v) = %v, want %v", process.Pid(), row.refusal, got, row.want)
		}
	}
}
