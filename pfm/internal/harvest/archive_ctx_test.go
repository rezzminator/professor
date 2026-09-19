package harvest

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTestZip writes a zip with the given name/content pairs and returns its
// path.
func buildTestZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestListArchiveContextStopsOnCancelledContext pins F24: listing ignored
// cancellation and enumerated every member regardless. Watched FAILING
// before the fix (a cancelled ctx still returned the full member list, nil
// error).
func TestListArchiveContextStopsOnCancelledContext(t *testing.T) {
	path := buildTestZip(t, map[string]string{"a.txt": "one", "b.txt": "two", "c.txt": "three"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := listArchiveContext(ctx, path)
	if err == nil {
		t.Fatal("listArchiveContext(cancelled) returned a nil error (F24)")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("listArchiveContext(cancelled) error = %v, want context.Canceled", err)
	}
}

// TestListArchiveContextRunsToCompletionWhenNotCancelled is the regression
// guard beside the test above: an ordinary (uncancelled) listing must keep
// working exactly as before.
func TestListArchiveContextRunsToCompletionWhenNotCancelled(t *testing.T) {
	path := buildTestZip(t, map[string]string{"a.txt": "one", "b.txt": "two"})
	members, err := listArchiveContext(context.Background(), path)
	if err != nil {
		t.Fatalf("listArchiveContext: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("listArchiveContext members = %#v, want 2", members)
	}
}

// TestReadArchiveMemberContextStopsOnCancelledContext pins F24's read half:
// a cancelled ctx must stop the member search/extraction rather than
// decompressing up to the caps anyway. Watched FAILING before the fix.
func TestReadArchiveMemberContextStopsOnCancelledContext(t *testing.T) {
	path := buildTestZip(t, map[string]string{"a.txt": strings.Repeat("x", 4096)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := readArchiveMemberContext(ctx, path, "a.txt")
	if err == nil {
		t.Fatal("readArchiveMemberContext(cancelled) returned a nil error (F24)")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("readArchiveMemberContext(cancelled) error = %v, want context.Canceled", err)
	}
}

// TestReadArchiveMemberContextRunsToCompletionWhenNotCancelled is the
// regression guard beside the test above.
func TestReadArchiveMemberContextRunsToCompletionWhenNotCancelled(t *testing.T) {
	path := buildTestZip(t, map[string]string{"a.txt": "hello"})
	body, err := readArchiveMemberContext(context.Background(), path, "a.txt")
	if err != nil || string(body) != "hello" {
		t.Fatalf("readArchiveMemberContext = %q err=%v", body, err)
	}
}

// TestHarvesterArchiveHonorsCancelledContext exercises the real *Harvester
// entry points (Archive/archiveList), the callers listArchiveContext and
// readArchiveMemberContext exist for.
func TestHarvesterArchiveHonorsCancelledContext(t *testing.T) {
	path := buildTestZip(t, map[string]string{"a.txt": "one", "b.txt": "two"})
	h := mustNew(t, Options{CacheDir: t.TempDir(), LocalRoots: []string{filepath.Dir(path)}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.Archive(ctx, path, "")
	if err == nil {
		t.Fatal("Harvester.Archive(cancelled) returned a nil error (F24)")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Harvester.Archive(cancelled) error = %v, want context.Canceled", err)
	}
}
