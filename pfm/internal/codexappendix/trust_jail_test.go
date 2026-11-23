//go:build jail

package codexappendix

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// The subject is the real helper's process group: a cancelled exchange must
// leave no member of it alive, which only a real child can show.
func TestRPCCancelKillsTheHelpersProcessGroup(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "pids")
	binary := filepath.Join(root, "fake-codex")
	// The descendant's output leaves the helper's pipe: a tail writing to a
	// closed pipe ends by itself, which would pass for the group kill.
	script := "#!/bin/sh\ntail -f /dev/null </dev/null >/dev/null 2>&1 &\n" +
		"echo \"$$ $!\" > '" + pidFile + ".tmp' && mv '" + pidFile + ".tmp' '" + pidFile + "'\n" +
		"while IFS= read -r line; do :; done\n"
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var pids []int
	t.Cleanup(func() {
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		_, err := rpc(ctx, binary, root, "hooks/list", map[string]any{})
		returned <- err
	}()
	testwait.Until(t, "the fake app-server to write "+pidFile, func() (bool, string) {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return false, err.Error()
		}
		for _, field := range strings.Fields(string(raw)) {
			pid, convErr := strconv.Atoi(field)
			if convErr != nil {
				t.Fatalf("parse pid %q from %s: %v", field, pidFile, convErr)
			}
			pids = append(pids, pid)
		}
		return len(pids) > 0, "no pids yet"
	})
	if len(pids) != 2 {
		t.Fatalf("%s holds pids %v, want the app-server and its descendant", pidFile, pids)
	}
	for _, pid := range pids {
		if err := syscall.Kill(pid, 0); err != nil {
			t.Fatalf("pid %d of the helper's group before the cancel: %v, want alive", pid, err)
		}
	}
	cancel()
	err, _ := testwait.Receive(t, "the rpc to return after its cancellation", returned)
	if want := "native hook API: context canceled"; err == nil || err.Error() != want {
		t.Fatalf("rpc after cancel = %v, want %q", err, want)
	}
	for _, pid := range pids {
		testwait.Until(t, fmt.Sprintf("pid %d of the helper's group to die after the cancelled exchange", pid),
			func() (bool, string) {
				killErr := syscall.Kill(pid, 0)
				return errors.Is(killErr, syscall.ESRCH), fmt.Sprint(killErr)
			})
	}
}
