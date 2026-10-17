package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

func TestClaudeMCPLockCannotReviveAfterNativeStaleWindow(t *testing.T) {
	previous := clock.Real
	fake := clock.NewFake(time.Now())
	clock.Real = fake
	t.Cleanup(func() { clock.Real = previous })
	path := filepath.Join(t.TempDir(), ".claude.json")
	writeFixture(t, path, "native registry")
	err := withClaudeMCPLocks([]string{path}, func(check func() error) error {
		fake.Advance(11 * time.Second)
		if err := check(); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("pfm overwrote expired ownership"), 0o600)
	})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expired native ownership revived: %v", err)
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != "native registry" {
		t.Fatalf("write after native stale window: %s", raw)
	}
}
