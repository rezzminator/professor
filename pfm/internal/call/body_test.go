package call

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

func TestPersistBodyPrunesExpiredMarkdownByAge(t *testing.T) {
	engine := newTestEngine(t, "cc-body-prune", &fakeTmux{capture: "❯ "})
	now := time.Date(2031, 2, 3, 4, 5, 6, 7, time.UTC)
	engine.options.Clock = fixedClock{Clock: clock.Real, now: now}
	engine.options.BodyMaxAge = 24 * time.Hour
	if err := os.MkdirAll(engine.options.BodyRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(engine.options.BodyRoot, "stale.md")
	fresh := filepath.Join(engine.options.BodyRoot, "fresh.md")
	nonBody := filepath.Join(engine.options.BodyRoot, "stale.txt")
	for _, path := range []string{stale, fresh, nonBody} {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(nonBody, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fresh, now, now); err != nil {
		t.Fatal(err)
	}
	path, warnings, err := engine.persistBody("exact body", "cc-1:%2")
	if err != nil || len(warnings) != 0 {
		t.Fatalf("persist body path=%q warnings=%q err=%v", path, warnings, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expired markdown survived: %v", err)
	}
	for _, kept := range []string{fresh, nonBody} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("non-expired/non-body file %q was pruned: %v", kept, err)
		}
	}
	stored, err := os.ReadFile(path)
	if err != nil || string(stored) != "exact body" ||
		!strings.Contains(filepath.Base(path), "cc-1--2") {
		t.Fatalf("canonical file path=%q body=%q err=%v", path, stored, err)
	}
}
