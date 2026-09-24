package harvest

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// TestPrivateHandleDirSurvivesConcurrentCreation: two search_literature calls on a
// fresh cache race to create .private/handles; the loser of the race must see
// the directory the winner made, never fail with "file exists".
func TestPrivateHandleDirSurvivesConcurrentCreation(t *testing.T) {
	for round := range 50 {
		root := filepath.Join(t.TempDir(), "cache")
		h := &Harvester{options: Options{CacheDir: root}}
		dir := filepath.Join(root, ".private", "handles")
		start := make(chan struct{})
		errs := make(chan error, 16)
		var wg sync.WaitGroup
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs <- h.ensurePrivateHandleDir(root, dir)
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: ensurePrivateHandleDir raced: %v", round, err)
			}
		}
	}
}

// TestPublicNamespaceSurvivesConcurrentCreation: the same race on public/.
func TestPublicNamespaceSurvivesConcurrentCreation(t *testing.T) {
	for round := range 50 {
		root := t.TempDir()
		start := make(chan struct{})
		errs := make(chan error, 16)
		var wg sync.WaitGroup
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, err := publicNamespace(root, true); err != nil {
					errs <- fmt.Errorf("publicNamespace: %w", err)
				}
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("round %d: %v", round, err)
		}
	}
}
