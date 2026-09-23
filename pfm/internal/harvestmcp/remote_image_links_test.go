package harvestmcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// TestRemoteReadPageImageLinksCarryNoServerPath pins A3 defect 3 at the
// remote gateway: readPage on a cached page with images answers content whose
// image links name no server path — neither the private cache path the stored
// page links (an older entry's absolute link, the localizer's page-relative
// one) nor the public directory the images are published into.
func TestRemoteReadPageImageLinksCarryNoServerPath(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	const source = "https://example.test/thread"
	pagePath := filepath.Join(cacheDir, harvest.CacheKey(source, "html"))
	imageDir := filepath.Join(cacheDir, "png")
	if err := os.MkdirAll(imageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32)
	for _, name := range []string{"a.png", "b.png"} {
		if err := os.WriteFile(filepath.Join(imageDir, name), []byte(png), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	body := strings.Repeat("A thread about something. ", 20) +
		"\n\n![a](" + filepath.Join(imageDir, "a.png") + ")\n\n![b](../png/b.png)\n"
	raw := "---\nurl: " + source + "\nfetched_at: " + time.Now().UTC().Format(time.RFC3339) +
		"\nsource: harvester\nmethod: direct\n---\n\n" + body
	if err := os.WriteFile(pagePath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	session := connectHarvesterInProcess(t, newTestService(t, Runtime{Remote: true, CacheDir: cacheDir}))
	var out PagesOutput
	text := callStructured(t, session, toolReadPage, map[string]any{"sources": []string{source}}, &out)
	if len(out.Items) != 1 || out.Items[0].Error != "" {
		t.Fatalf("readPage items = %+v", out.Items)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for what, got := range map[string]string{"structured": string(encoded), "text": text} {
		if strings.Contains(got, cacheDir) || strings.Contains(got, "](/") {
			t.Fatalf("remote readPage %s result carries a server path (cache %q): %s", what, cacheDir, got)
		}
	}
	if n := strings.Count(out.Items[0].Content, "](./"); n != 2 {
		t.Fatalf("published image links = %d, want 2: %q", n, out.Items[0].Content)
	}
}
