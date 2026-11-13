//go:build jail

package gather

import (
	"os/exec"
	"sync"
	"testing"
)

// Acquire-and-close loops running beside goroutines that start processes never
// see a busy account: a fork between open and exec must not fail the guard.
func TestAcquireAccountGuardSurvivesConcurrentProcessStarts(t *testing.T) {
	dir := t.TempDir()
	stop := make(chan struct{})
	var starters sync.WaitGroup
	for range 4 {
		starters.Add(1)
		go func() {
			defer starters.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = exec.Command("true").Run()
				}
			}
		}()
	}
	defer func() {
		close(stop)
		starters.Wait()
	}()
	for attempt := range 400 {
		guard, err := AcquireAccountGuard(dir, false)
		if err != nil {
			t.Fatalf("acquire %d beside process starts: %v", attempt, err)
		}
		if err := guard.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
