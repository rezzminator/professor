package testjail

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// A fork elsewhere in the process while a fixture is open for writing inherits
// its write descriptor; exec of that fixture then fails with ETXTBSY ("text
// file busy") until the forked child execs. Background goroutines fork
// continuously here while fixtures are written and run at once.
func TestWriteExecutableRunsWhileTheProcessForks(t *testing.T) {
	dir := t.TempDir()
	stop := make(chan struct{})
	var forks sync.WaitGroup
	for range 4 {
		forks.Add(1)
		go func() {
			defer forks.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := exec.Command("true").Run(); err != nil {
					t.Errorf("background fork: %v", err)
					return
				}
			}
		}()
	}
	defer func() {
		close(stop)
		forks.Wait()
	}()

	for index := range 100 {
		fixture := filepath.Join(dir, fmt.Sprintf("fixture-%d", index))
		WriteExecutable(t, fixture, []byte("#!/bin/sh\nexit 0\n"))
		if output, err := exec.Command(fixture).CombinedOutput(); err != nil {
			t.Fatalf("run fixture %d right after writing it: %v: %s", index, err, output)
		}
	}
}
