package installer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeMCPLocksReclaimOnlyStaleDirectories(t *testing.T) {
	for _, state := range []string{"stale", "fresh", "stale nonempty", "stale file"} {
		t.Run(state, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".claude.json")
			lock := path + ".lock"
			if state == "stale file" {
				writeFixture(t, lock, "operator")
			} else if err := os.Mkdir(lock, 0o700); err != nil {
				t.Fatal(err)
			}
			if state == "stale nonempty" {
				writeFixture(t, filepath.Join(lock, "owner"), "native")
			}
			if state != "fresh" {
				old := time.Now().Add(-11 * time.Second)
				if err := os.Chtimes(lock, old, old); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			err := withClaudeMCPLocks([]string{path}, func(check func() error) error { called = true; return check() })
			if state == "stale" {
				if err != nil || !called {
					t.Fatalf("stale native lock was not reclaimed: %v", err)
				}
				if _, err := os.Lstat(lock); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("lock not released: %v", err)
				}
			} else {
				if err == nil || called {
					t.Fatalf("unsafe lock accepted: %v", err)
				}
				if state != "fresh" &&
					(!strings.Contains(err.Error(), "stale") || strings.Contains(err.Error(), "close the native writer")) {
					t.Fatalf("misleading stale diagnostic: %v", err)
				}
				if _, err := os.Lstat(lock); err != nil {
					t.Fatalf("native lock removed: %v", err)
				}
			}
		})
	}
}
