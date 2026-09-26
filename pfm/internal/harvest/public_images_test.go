package harvest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicImagePathResolvesLocalRefsAndRefusesBareNames(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		basePath string
		wantPath string
		wantOK   bool
	}{
		{
			name:     "absolute path resolves regardless of basePath",
			raw:      "/srv/docs/fig1.png",
			basePath: "",
			wantPath: "/srv/docs/fig1.png",
			wantOK:   true,
		},
		{
			name:     "file:// URL resolves through fileURLPath",
			raw:      "file:///srv/docs/fig2.png",
			basePath: "",
			wantPath: "/srv/docs/fig2.png",
			wantOK:   true,
		},
		{
			name:     "relative path with a directory separator joins basePath's dir",
			raw:      "images/fig3.png",
			basePath: "/srv/docs/report.md",
			wantPath: filepath.Join(string(filepath.Separator), "srv", "docs", "images", "fig3.png"),
			wantOK:   true,
		},
		{
			name:     "dot-relative path joins basePath's dir even with no separator",
			raw:      "./fig4.png",
			basePath: "/srv/docs/report.md",
			wantPath: filepath.Join(string(filepath.Separator), "srv", "docs", "fig4.png"),
			wantOK:   true,
		},
		{
			name:     "bare filename with no basePath is refused, not guessed at",
			raw:      "fig5.png",
			basePath: "",
			wantPath: "",
			wantOK:   false,
		},
		{
			name:     "query and fragment are stripped before resolving",
			raw:      "/srv/docs/fig6.png?raw=true#section",
			basePath: "",
			wantPath: "/srv/docs/fig6.png",
			wantOK:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPath, gotOK := publicImagePath(tc.raw, tc.basePath)
			if gotOK != tc.wantOK || gotPath != tc.wantPath {
				t.Fatalf(
					"publicImagePath(%q, %q) = (%q, %v), want (%q, %v)",
					tc.raw,
					tc.basePath,
					gotPath,
					gotOK,
					tc.wantPath,
					tc.wantOK,
				)
			}
		})
	}
}

const pngMagic = "\x89PNG\r\n\x1a\n" + "rest-of-a-fake-png-body"

// TestRewritePublicImagesCopiesLocalImageIntoPublicNamespace pins the
// success path: a markdown image referencing a real local PNG that lives
// inside the harvester's cache (but outside its public/ namespace) is copied
// into public/ and the markdown link is rewritten to the new public path.
func TestRewritePublicImagesCopiesLocalImageIntoPublicNamespace(t *testing.T) {
	setHarvestTestJail(t)
	cacheDir := t.TempDir()
	h := mustNew(t, Options{CacheDir: cacheDir})

	imgPath := filepath.Join(cacheDir, "fig1.png")
	if err := os.WriteFile(imgPath, []byte(pngMagic), 0o600); err != nil {
		t.Fatal(err)
	}

	body := "See the diagram: ![figure one](" + imgPath + ") for details."
	rewritten, _, err := h.rewritePublicImages("report.md", body, "")
	if err != nil {
		t.Fatalf("rewritePublicImages error: %v", err)
	}
	if strings.Contains(rewritten, imgPath) {
		t.Fatalf("rewritePublicImages leaked the private cache path: %q", rewritten)
	}
	publicRoot, err := h.publicRoot()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rewritten, "](./") || strings.Contains(rewritten, publicRoot) {
		t.Fatalf("rewritePublicImages did not rewrite to a public-relative link: %q (root=%q)", rewritten, publicRoot)
	}

	// Extract the rewritten link and verify the bytes were actually copied.
	start := strings.Index(rewritten, "](") + 2
	end := strings.Index(rewritten[start:], ")")
	publicPath := filepath.Join(publicRoot, rewritten[start:start+end])
	data, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatalf("reading copied public artifact: %v", err)
	}
	if string(data) != pngMagic {
		t.Fatalf("copied public artifact content = %q, want %q", data, pngMagic)
	}
}

// TestRewritePublicImagesSkipsRemoteAndDataURIs pins that http(s):// and
// data: image references are left untouched — only local file references are
// resolved and copied.
func TestRewritePublicImagesSkipsRemoteAndDataURIs(t *testing.T) {
	setHarvestTestJail(t)
	h := mustNew(t, Options{CacheDir: t.TempDir()})
	// basePath is non-empty on purpose: both raw refs below contain a "/",
	// so without the http(s)/data: skip guard, publicImagePath would treat
	// them as basePath-relative local paths instead of leaving them alone.
	const basePath = "/srv/docs/report.md"
	body := "![remote](https://example.test/a.png) ![inline](data:image/png;base64,AAAA)"
	rewritten, _, err := h.rewritePublicImages(basePath, body, basePath)
	if err != nil {
		t.Fatalf("rewritePublicImages error: %v", err)
	}
	if rewritten != body {
		t.Fatalf("rewritePublicImages altered remote/data URIs: got %q, want unchanged %q", rewritten, body)
	}
}

// TestRewritePublicImagesRefusesPrivateMetadataPaths pins the leak gate: an
// image reference that resolves inside the cache's private metadata
// namespaces (.private, stats, auth, handles, searchcache) is refused rather
// than silently exported: the image is dropped, its alt text kept, and no
// trace of the private path is published.
func TestRewritePublicImagesRefusesPrivateMetadataPaths(t *testing.T) {
	setHarvestTestJail(t)
	cacheDir := t.TempDir()
	h := mustNew(t, Options{CacheDir: cacheDir})

	privateDir := filepath.Join(cacheDir, ".private")
	if err := os.MkdirAll(privateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	imgPath := filepath.Join(privateDir, "secret.png")
	if err := os.WriteFile(imgPath, []byte(pngMagic), 0o600); err != nil {
		t.Fatal(err)
	}

	body := "![leak](" + imgPath + ")"
	rewritten, dropped, err := h.rewritePublicImages("report.md", body, "")
	if err != nil {
		t.Fatalf("rewritePublicImages error: %v", err)
	}
	if dropped != 1 || strings.Contains(rewritten, imgPath) || rewritten != "leak" {
		t.Fatalf(
			"rewritePublicImages did not refuse a private-metadata image path: dropped=%d body=%q",
			dropped,
			rewritten,
		)
	}
}

// writeCachedImage stores a PNG under cacheDir at the name the localizer
// (storeBinary via CacheKey) writes for remote, and returns its path.
func writeCachedImage(t *testing.T, cacheDir, remote string) string {
	t.Helper()
	path := filepath.Join(cacheDir, strings.TrimSuffix(CacheKey(remote, "png"), ".md")+".png")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(pngMagic), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeCachedPage stores body as a harvester cache document and returns the
// Result the fetch core hands PublicResult for it.
func writeCachedPage(t *testing.T, cacheDir, source, body string) Result {
	t.Helper()
	path := filepath.Join(cacheDir, CacheKey(source, "html"))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := "---\nurl: " + source + "\nfetched_at: 2025-01-02T03:04:05Z\nsource: harvester\nmethod: direct\n---\n\n" + body
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return Result{Source: source, Kind: "html", Path: path, Method: "direct", CacheStatus: "miss"}
}

// TestPublicResultRelocatesHebrewAndPercentEncodedImages pins the he.wikipedia
// failure: images whose remote names are Hebrew or percent-encoded are all
// relocated into public/, and a protocol-relative (//host/…) link the
// localizer left remote is a remote link, never a local path to refuse.
func TestPublicResultRelocatesHebrewAndPercentEncodedImages(t *testing.T) {
	setHarvestTestJail(t)
	cacheDir := t.TempDir()
	h := mustNew(t, Options{CacheDir: cacheDir})
	hebrew := writeCachedImage(
		t,
		cacheDir,
		"https://thumb.wikimedia.org/wikipedia/he/thumb/6/60/תחבורת_ירושלים.png/330px-תחבורת_ירושלים.png",
	)
	encoded := writeCachedImage(
		t,
		cacheDir,
		"https://thumb.wikimedia.org/wikipedia/he/thumb/6/60/%D7%AA%D7%97.png/330px-%D7%AA%D7%97.png?utm_source=he.wikipedia.org",
	)
	remote := "//thumb.wikimedia.org/wikipedia/commons/thumb/9/98/%D7%AA%D7%97.jpg/330px-%D7%AA%D7%97.jpg?utm_source=he.wikipedia.org"
	body := strings.Repeat("ירושלים היא עיר הבירה. ", 20) + "\n\n![סמל](" + hebrew + ")\n\n![קידוד](" + encoded +
		")\n\n![תחנה](" + remote + ")\n"

	got := h.PublicResult(
		"https://he.wikipedia.org/wiki/ירושלים",
		writeCachedPage(t, cacheDir, "https://he.wikipedia.org/wiki/ירושלים", body),
		false,
	)
	if got.Error != "" {
		t.Fatalf("PublicResult() error = %q", got.Error)
	}
	publicRoot, err := h.publicRoot()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Content, hebrew) || strings.Contains(got.Content, encoded) {
		t.Fatalf("a cached image link was not relocated: %q", got.Content)
	}
	if n := strings.Count(got.Content, "](./"); n != 2 || strings.Contains(got.Content, publicRoot) {
		t.Fatalf("relocated image links = %d, want 2: %q", n, got.Content)
	}
	if !strings.Contains(got.Content, "![תחנה]("+remote+")") || got.Partial != "" {
		t.Fatalf("the remote link was altered or the page flagged partial (%q): %q", got.Partial, got.Content)
	}
}

// TestPublicResultDropsAnImageThatCannotBePublished pins the Lemmy failure: a
// site-relative image link no localizer stored is dropped from the published
// page, its alt text kept and the drop named in the partial, while the rest of
// the page and its other images are published.
func TestPublicResultDropsAnImageThatCannotBePublished(t *testing.T) {
	setHarvestTestJail(t)
	cacheDir := t.TempDir()
	h := mustNew(t, Options{CacheDir: cacheDir})
	good := writeCachedImage(t, cacheDir, "https://lemmy.world/pictrs/image/a.jpeg")
	body := strings.Repeat("A post about something. ", 20) + "\n\n![post image](" + good +
		")\n\n[![site icon](/static/a406e80b/assets/icons/icon-96x96.png)](https://lemmy.world/)\n"

	got := h.PublicResult(
		"https://lemmy.world/post/1",
		writeCachedPage(t, cacheDir, "https://lemmy.world/post/1", body),
		false,
	)
	if got.Error != "" {
		t.Fatalf("PublicResult() error = %q", got.Error)
	}
	if strings.Contains(got.Content, "/static/a406e80b") || strings.Contains(got.Content, good) {
		t.Fatalf("an unpublishable or private image link survived: %q", got.Content)
	}
	if !strings.Contains(got.Content, "[site icon](https://lemmy.world/)") {
		t.Fatalf("the dropped image's alt text was not kept: %q", got.Content)
	}
	if !strings.Contains(got.Partial, "1 image(s) could not be published") {
		t.Fatalf("partial = %q, want the dropped image named", got.Partial)
	}
	stored, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), partialMarkerPrefix+"1 image(s) could not be published") {
		t.Fatalf("the public artifact does not carry the partial marker: %q", stored)
	}
}

// TestPublicExportFailureSaysPermanentOrTransient pins the failure wording: an
// export step that fails the same way on every retry names itself and never
// says "Retry later"; a storage read that can recover still does.
func TestPublicExportFailureSaysPermanentOrTransient(t *testing.T) {
	setHarvestTestJail(t)
	cacheDir := t.TempDir()
	h := mustNew(t, Options{CacheDir: cacheDir})

	permanent := h.PublicResult("https://example.test/a", Result{Source: "https://example.test/a", Kind: "html"}, false)
	if permanent.Error == "" || strings.Contains(permanent.Error, "Retry later") ||
		!strings.Contains(permanent.Error, "repeats on every retry") ||
		!strings.Contains(permanent.Error, "publish result without complete artifact") {
		t.Fatalf("permanent export failure = %q (kind %q)", permanent.Error, permanent.ErrorKind)
	}

	missing := filepath.Join(cacheDir, CacheKey("https://example.test/b", "html"))
	transient := h.PublicResult(
		"https://example.test/b",
		Result{Source: "https://example.test/b", Kind: "html", Path: missing},
		false,
	)
	if !strings.Contains(transient.Error, "Retry later") {
		t.Fatalf("transient export failure = %q, want Retry later", transient.Error)
	}
}
