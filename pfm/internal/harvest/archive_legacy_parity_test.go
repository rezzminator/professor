package harvest

// This file is the Go regression oracle for the RETIRED Python harvester's
// test_dispatch_hardening.py case that a binary OA candidate is never cached
// as text (harvester/ was deleted after full migration to pfm; read that suite
// at its last tracked revision, `git show 4edf9c5^:harvester/tests/...`). The
// archive member readers it once also pinned are gone: a zip is a download.

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyParityBinaryArchiveIsNotCachedAsText(t *testing.T) {
	var zipBytes bytes.Buffer
	zw := zip.NewWriter(&zipBytes)
	w, err := zw.Create("book.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(bytes.Repeat([]byte("x"), 2000)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	h := mustNew(
		t,
		Options{
			CacheDir: cacheDir,
			Client:   legacyArchiveClient(http.StatusOK, "application/epub+zip", zipBytes.Bytes()),
			Chrome:   legacyArchiveClient(http.StatusOK, "application/epub+zip", zipBytes.Bytes()),
		},
	)
	// fetchURL is the closest boundary to the OA-candidate path and must never
	// turn ZIP bytes into .md.
	got := h.fetchURL(context.Background(), "https://web.archive.org/book.epub", FetchOptions{})
	if got.Error == "" {
		t.Fatalf("binary candidate = %#v; want rejection without converter", got)
	}
	if files := walkLegacyExt(t, cacheDir, ".md"); len(files) != 0 {
		t.Fatalf("binary archive cached as text: %v", files)
	}
}

func legacyArchiveClient(status int, contentType string, body []byte) *http.Client {
	return &http.Client{Transport: legacyRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     http.Header{"Content-Type": []string{contentType}},
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    r,
		}, nil
	})}
}

func walkLegacyExt(t *testing.T, root, ext string) []string {
	t.Helper()
	var found []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ext) {
			found = append(found, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return found
}

type legacyRoundTrip func(*http.Request) (*http.Response, error)

func (f legacyRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
