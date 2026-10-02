package testjail

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// startForkLoad starts 4 goroutines forking and execing true until the returned
// stop function is called; stop also waits for them.
func startForkLoad(t *testing.T) (stop func()) {
	t.Helper()
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Fatalf("fork load needs true: %v", err)
	}
	done := make(chan struct{})
	var forks sync.WaitGroup
	for range 4 {
		forks.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				_ = exec.Command(trueBinary).Run()
			}
		})
	}
	return func() {
		close(done)
		forks.Wait()
	}
}

// A script written by WriteExecutable runs right after the write while other
// goroutines of this process fork. With a bare os.WriteFile a fork inside the
// write window keeps the write descriptor open in the child and the exec fails
// with "text file busy" within the first few hundred runs.
func TestWriteExecutableRunsWhileOtherGoroutinesFork(t *testing.T) {
	dir := t.TempDir()
	defer startForkLoad(t)()
	deadline := time.Now().Add(2 * time.Second)
	for index := 0; index < 1000 && time.Now().Before(deadline); index++ {
		target := filepath.Join(dir, fmt.Sprintf("script-%d", index))
		if err := WriteExecutable(target, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatalf("write %d: %v", index, err)
		}
		if output, err := exec.Command(target).CombinedOutput(); err != nil {
			t.Fatalf("run script %d right after writing it, beside forking goroutines: %v %s", index, err, output)
		}
	}
}
