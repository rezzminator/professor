package deps

import (
	"context"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// TestRunProcessGroupGivesTheChildItsOwnGroup proves RunOptions.ProcessGroup
// starts the child in a process group of its own, so a terminal's Ctrl-C to
// the caller's group never reaches it; without it the child shares the
// caller's group.
func TestRunProcessGroupGivesTheChildItsOwnGroup(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skipf("no /proc to read a child's process group: %v", err)
	}
	argv := []string{"sh", "-c", `read -r _ _ _ _ pgid _ </proc/$$/stat; echo "$$ $pgid"`}
	for _, own := range []bool{true, false} {
		result, err := RealRunner{}.Run(context.Background(), argv, RunOptions{ProcessGroup: own})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("ProcessGroup=%v: run err %v exit %d stderr %q", own, err, result.ExitCode, result.Stderr)
		}
		fields := strings.Fields(string(result.Stdout))
		if len(fields) != 2 {
			t.Fatalf("ProcessGroup=%v: output %q, want pid and pgid", own, result.Stdout)
		}
		pgid, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatalf("ProcessGroup=%v: pgid %q: %v", own, fields[1], err)
		}
		if leader := fields[0] == fields[1]; leader != own || !own && pgid != syscall.Getpgrp() {
			t.Fatalf("ProcessGroup=%v: child pid/pgid %q, caller pgid %d", own, result.Stdout, syscall.Getpgrp())
		}
	}
}
