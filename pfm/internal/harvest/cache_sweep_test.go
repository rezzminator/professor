package harvest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

type agedCacheFile struct {
	rel  string
	age  time.Duration
	size int
	kept bool
}

func writeAgedCacheFile(t *testing.T, root string, file agedCacheFile, now time.Time) string {
	t.Helper()
	path := filepath.Join(root, file.rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", file.size)), 0o600); err != nil {
		t.Fatal(err)
	}
	at := now.Add(-file.age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSweepCacheRoot pins the sweep's rules: every kind and public/ expire
// 30 days after their last write; past that the oldest go until the cache
// fits its cap; a file younger than an hour (a write in flight) never goes,
// even over the cap.
func TestSweepCacheRoot(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		name     string
		maxBytes int64
		files    []agedCacheFile
		freed    int64
		linked   bool // the cache root is reached through a symlink
	}{
		{"age expires every kind and public", 1 << 20, []agedCacheFile{
			{"html/old.md", 31 * day, 10, false},
			{"public/0a1b.jpg", 31 * day, 20, false},
			{"caller-headers/ab12/pdf/doc.md", 40 * day, 30, false},
			{"public/0c2d.png", 10 * day, 40, true},
			{"pdf/recent.md", 29 * day, 50, true},
			{".harvest-123", 31 * day, 5, false},
		}, 65, false},
		{"cap evicts oldest first", 900, []agedCacheFile{
			{"jpg/a.jpg", 5 * day, 400, false},
			{"png/b.png", 3 * day, 400, false},
			{"html/c.md", 2 * day, 400, true},
			{"public/d.webp", 30 * time.Minute, 400, true},
		}, 800, false},
		{"a write in flight is never evicted over the cap", 500, []agedCacheFile{
			{"public/e.pdf", 30 * time.Minute, 1000, true},
			{".public-777", 10 * time.Minute, 100, true},
		}, 0, false},
		{"a symlinked cache root is swept through its link", 1 << 20, []agedCacheFile{
			{"html/old.md", 31 * day, 10, false},
			{"png/new.png", day, 5, true},
		}, 10, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			now := time.Now()
			paths := map[string]agedCacheFile{}
			for _, file := range tc.files {
				paths[writeAgedCacheFile(t, root, file, now)] = file
			}
			swept := root
			if tc.linked {
				swept = filepath.Join(t.TempDir(), "cache-link")
				if err := os.Symlink(root, swept); err != nil {
					t.Fatal(err)
				}
			}
			result := sweepCacheRoot(swept, now, cacheMaxAge, tc.maxBytes)
			removed := 0
			for path, file := range paths {
				_, err := os.Lstat(path)
				if file.kept != (err == nil) {
					t.Errorf("%s kept=%v, want %v (err=%v)", file.rel, err == nil, file.kept, err)
				}
				if !file.kept {
					removed++
				}
			}
			if result.removed != removed || result.freed != tc.freed || result.failed != 0 {
				t.Fatalf("result=%+v, want removed=%d freed=%d", result, removed, tc.freed)
			}
		})
	}
}

// TestCacheWritesSweepAtMostDaily pins the trigger: a cache write sweeps when
// the stamp is absent or a day old, and a write within the day does not.
func TestCacheWritesSweepAtMostDaily(t *testing.T) {
	writes := map[string]func(h *Harvester, root string) error{
		"markdown result": func(h *Harvester, _ string) error {
			if result := h.storeResult("https://example.test/a", kindHTML, "direct", "body", 4, 200, nil,
				FetchOptions{}); result.Error != "" {
				return errors.New(result.Error)
			}
			return nil
		},
		"public artifact": func(h *Harvester, root string) error {
			return h.writeAtomic(filepath.Join(root, publicDirName, "f00d.png"), []byte("png"), 0o600)
		},
		"binary download": func(h *Harvester, _ string) error {
			if got := h.Download(context.Background(), "https://203.0.113.10/figure.png"); got.Error != "" {
				return errors.New(got.Error)
			}
			return nil
		},
		"browser download": func(h *Harvester, _ string) error {
			got := h.Download(context.Background(), "https://203.0.113.10/paper.pdf")
			if got.Error != "" || got.Method != rungBrowserDownload {
				return fmt.Errorf("browser download: method=%q error=%q", got.Method, got.Error)
			}
			return nil
		},
	}
	// .png is served directly; anything else meets a challenge wall, no
	// Wayback copy, and the browser rung downloads it.
	serve := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, ".png") {
			return response(request, http.StatusOK, "image/png", "\x89PNG\r\n\x1a\n"+strings.Repeat("\x00", 64)), nil
		}
		return response(request, http.StatusForbidden, "text/html", challengePage), nil
	})
	noCopy := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(request, `{"archived_snapshots":{}}`), nil
	})
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			fake := clock.NewFake(time.Now())
			h, err := New(Options{
				CacheDir: root, Clock: fake,
				Client: &http.Client{Transport: serve}, Chrome: &http.Client{Transport: serve},
				OA:          &http.Client{Transport: noCopy},
				Converter:   &downloadingBrowser{body: "%PDF-1.7\nbrowser bytes", contentType: "application/pdf"},
				BrowserRung: browserOn(),
			})
			if err != nil {
				t.Fatal(err)
			}
			old := agedCacheFile{"jpg/old.jpg", 31 * 24 * time.Hour, 3, false}
			first := writeAgedCacheFile(t, root, old, fake.Now())
			if err := write(h, root); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(first); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("first write did not sweep: %v", err)
			}
			second := writeAgedCacheFile(t, root, old, fake.Now())
			fake.Advance(time.Hour)
			if err := write(h, root); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(second); err != nil {
				t.Fatalf("a write within the day swept again: %v", err)
			}
			fake.Advance(cacheSweepInterval)
			if err := write(h, root); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(second); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a write a day later did not sweep: %v", err)
			}
		})
	}
}

// TestCacheSweepFailureOnlyLogs pins the failure door: a failed removal is
// a warn record naming the path, the file stays, and the write that
// triggered the sweep still succeeds.
func TestCacheSweepFailureOnlyLogs(t *testing.T) {
	_, recorder := obs.Test(t)
	root := t.TempDir()
	stuck := writeAgedCacheFile(t, root, agedCacheFile{"pdf/stuck.pdf", 31 * 24 * time.Hour, 3, true}, time.Now())
	restore := cacheSweepRemove
	cacheSweepRemove = func(path string) error {
		if path == stuck {
			return errors.New("permission denied")
		}
		return os.Remove(path)
	}
	t.Cleanup(func() { cacheSweepRemove = restore })
	h, err := New(Options{CacheDir: root})
	if err != nil {
		t.Fatal(err)
	}
	if result := h.storeResult("https://example.test/b", kindHTML, "direct", "body", 4, 200, nil,
		FetchOptions{}); result.Error != "" {
		t.Fatalf("write failed on a failed sweep removal: %s", result.Error)
	}
	if _, err := os.Lstat(stuck); err != nil {
		t.Fatalf("failed removal lost the file: %v", err)
	}
	for _, record := range recorder.Records() {
		if path, _ := record.Field("path"); record.Message == "harvest.cache_sweep" && path == stuck &&
			record.Level == "WARN" {
			return
		}
	}
	t.Fatalf("no warn record for the failed removal: %s", recorder.Raw())
}
