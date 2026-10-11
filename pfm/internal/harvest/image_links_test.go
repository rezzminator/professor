package harvest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// imageLinks is every markdown image link in body.
func imageLinks(body string) []string {
	var links []string
	for _, match := range markdownImageRE.FindAllStringSubmatch(body, -1) {
		links = append(links, match[1])
	}
	return links
}

// assertNoServerPath fails when body names the cache directory or links an
// image by an absolute path, and when a link does not resolve to a file from
// dir — the directory of the document body was stored in.
func assertNoServerPath(t *testing.T, what, body, cacheDir, dir string) {
	t.Helper()
	if strings.Contains(body, cacheDir) || strings.Contains(body, "](/") {
		t.Fatalf("%s carries a server path (cache %q): %q", what, cacheDir, body)
	}
	links := imageLinks(body)
	if len(links) == 0 {
		t.Fatalf("%s has no image link to check: %q", what, body)
	}
	for _, link := range links {
		if strings.HasPrefix(link, "http") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(link)))
		if err != nil || !strings.HasPrefix(string(data), "\x89PNG") {
			t.Fatalf("%s link %q does not resolve from %q: %v", what, link, dir, err)
		}
	}
}

// TestPublicResultImageLinksCarryNoServerPath: the published page — the
// content a remote read returns and the public file it stores — links its
// images relative to itself, both for a page-relative link and for an absolute
// one an older cache entry still carries (the harvester once localized a
// page's images into the cache; it no longer does).
func TestPublicResultImageLinksCarryNoServerPath(t *testing.T) {
	setHarvestTestJail(t)
	cacheDir := t.TempDir()
	h := mustNew(t, Options{CacheDir: cacheDir})
	const source = "https://example.test/thread"
	relative := writeCachedImage(t, cacheDir, "https://example.test/a.png")
	absolute := writeCachedImage(t, cacheDir, "https://example.test/b.png")
	pageDir := filepath.Dir(filepath.Join(cacheDir, CacheKey(source, kindHTML)))
	link, err := filepath.Rel(pageDir, relative) // the page-relative link an older cache entry stores
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("A thread about something. ", 20) + "\n\n![a](" + link + ")\n\n![b](" + absolute + ")\n"
	assertNoServerPath(t, "stored page (relative link)", "![a]("+link+")", cacheDir, pageDir)

	got := h.PublicResult(source, writeCachedPage(t, cacheDir, source, body), false)
	if got.Error != "" || got.Partial != "" {
		t.Fatalf("PublicResult() error = %q partial = %q", got.Error, got.Partial)
	}
	assertNoServerPath(t, "published content", got.Content, cacheDir, filepath.Dir(got.Path))
	stored, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	assertNoServerPath(t, "published file", string(stored), cacheDir, filepath.Dir(got.Path))
	if n := len(imageLinks(got.Content)); n != 2 {
		t.Fatalf("published image links = %d, want 2: %q", n, got.Content)
	}
}
