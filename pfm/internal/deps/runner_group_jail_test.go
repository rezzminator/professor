//go:build jail

package deps

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestRunProcessGroupGivesTheChildItsOwnGroup proves RunOptions.ProcessGroup
// starts the child in a process group of its own, so a terminal's Ctrl-C to
// the caller's group never reaches it, and RunOptions.Setsid in a session of
// its own as well, with no controlling terminal; without either the child
// shares the caller's group and session.
func TestRunProcessGroupGivesTheChildItsOwnGroup(t *testing.T) {
	procRoot := paths.SystemProcRoot()
	if _, err := os.Stat(filepath.Join(procRoot, "self", "stat")); err != nil {
		t.Skipf("no /proc to read a child's process group: %v", err)
	}
	argv := []string{"sh", "-c", `read -r _ _ _ _ pgid sid _ <"$1/$$/stat"; echo "$$ $pgid $sid"`, "sh", procRoot}
	for _, opts := range []RunOptions{{ProcessGroup: true}, {Setsid: true}, {}} {
		own := opts.ProcessGroup || opts.Setsid
		result, err := RealRunner{}.Run(context.Background(), argv, opts)
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("%+v: run err %v exit %d stderr %q", opts, err, result.ExitCode, result.Stderr)
		}
		fields := strings.Fields(string(result.Stdout))
		if len(fields) != 3 {
			t.Fatalf("%+v: output %q, want pid, pgid and sid", opts, result.Stdout)
		}
		if sessionLeader := fields[0] == fields[2]; sessionLeader != opts.Setsid {
			t.Fatalf("%+v: child pid/pgid/sid %q, want session leader %v", opts, result.Stdout, opts.Setsid)
		}
		pgid, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatalf("%+v: pgid %q: %v", opts, fields[1], err)
		}
		if leader := fields[0] == fields[1]; leader != own || !own && pgid != syscall.Getpgrp() {
			t.Fatalf("%+v: child pid/pgid/sid %q, caller pgid %d", opts, result.Stdout, syscall.Getpgrp())
		}
	}
}
