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
			wantPath: filepath.Join("/srv/docs", "images/fig3.png"),
			wantOK:   true,
		},
		{
			name:     "dot-relative path joins basePath's dir even with no separator",
			raw:      "./fig4.png",
			basePath: "/srv/docs/report.md",
			wantPath: filepath.Join("/srv/docs", "./fig4.png"),
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
	rewritten, err := h.rewritePublicImages("report.md", body, "")
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
	if !strings.Contains(rewritten, publicRoot) {
		t.Fatalf("rewritePublicImages did not rewrite into the public namespace: %q (root=%q)", rewritten, publicRoot)
	}

	// Extract the rewritten path and verify the bytes were actually copied.
	start := strings.Index(rewritten, "](") + 2
	end := strings.Index(rewritten[start:], ")")
	publicPath := rewritten[start : start+end]
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
	rewritten, err := h.rewritePublicImages(basePath, body, basePath)
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
// than silently exported.
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
	if _, err := h.rewritePublicImages("report.md", body, ""); err == nil {
		t.Fatal("rewritePublicImages did not refuse a private-metadata image path")
	}
}
