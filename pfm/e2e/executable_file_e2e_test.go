//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// writeExecutable writes a file this process or its children later execute.
// The read side of syscall.ForkLock keeps every fork of this process out while
// the file is open for writing: a child forked in that window inherits the
// write descriptor until its own exec closes it, and executing the file then
// fails with ETXTBSY ("text file busy"). Parallel tests fork constantly.
func writeExecutable(path string, body []byte, mode os.FileMode) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, body, mode)
}

func TestExecutableCopyRunsWhileOtherGoroutinesFork(t *testing.T) {
	t.Parallel()
	requireE2EFence(t)
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Fatalf("fork load needs true: %v", err)
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var forks sync.WaitGroup
	for range 4 {
		forks.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = exec.Command(trueBinary).Run()
			}
		})
	}
	defer forks.Wait()
	defer close(stop)
	// Unlocked, a copy fails within its first 20 runs here; a second of runs
	// keeps the fork load short beside the heavy tests.
	deadline := time.Now().Add(time.Second)
	for index := 0; index < 500 && time.Now().Before(deadline); index++ {
		target := filepath.Join(dir, fmt.Sprintf("copy-%d", index))
		if err := copyFile(source, target, 0o755); err != nil {
			t.Fatalf("copy %d: %v", index, err)
		}
		if output, err := exec.Command(target).CombinedOutput(); err != nil {
			t.Fatalf("run copy %d right after writing it, beside forking goroutines: %v %s", index, err, output)
		}
	}
}
