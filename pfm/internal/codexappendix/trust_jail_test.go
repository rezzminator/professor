package codexappendix

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// The subject is the real helper's process group: a cancelled exchange must
// leave no member of it alive, which only a real child can show.
func TestRPCCancelKillsTheHelpersProcessGroup(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "pids")
	binary := filepath.Join(root, "fake-codex")
	script := "#!/bin/sh\nsleep 60 &\necho \"$$ $!\" > '" + pidFile + ".tmp' && mv '" + pidFile + ".tmp' '" + pidFile + "'\n" +
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
	deadline := time.Now().Add(10 * time.Second)
	for len(pids) == 0 {
		if raw, err := os.ReadFile(pidFile); err == nil {
			for _, field := range strings.Fields(string(raw)) {
				pid, convErr := strconv.Atoi(field)
				if convErr != nil {
					t.Fatalf("parse pid %q from %s: %v", field, pidFile, convErr)
				}
				pids = append(pids, pid)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fake app-server never wrote %s", pidFile)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(pids) != 2 {
		t.Fatalf("%s holds pids %v, want the app-server and its descendant", pidFile, pids)
	}
	cancel()
	select {
	case err := <-returned:
		if want := "native hook API: context canceled"; err == nil || err.Error() != want {
			t.Fatalf("rpc after cancel = %v, want %q", err, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("rpc did not return within 10s of its cancellation")
	}
	deadline = time.Now().Add(250*time.Millisecond + time.Second)
	for _, pid := range pids {
		for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			if time.Now().After(deadline) {
				t.Fatalf("pid %d of the helper's group survived the cancelled exchange", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}
