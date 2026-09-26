package harvest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsPathInsideAcceptsDescendantsAndRefusesSiblings(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"exact root is inside itself", root, true},
		{"direct child is inside", filepath.Join(root, "child.txt"), true},
		{"nested descendant is inside", filepath.Join(root, "a", "b", "c.txt"), true},
		{"sibling directory sharing a name prefix is refused", root + "-sibling/x.txt", false},
		{"parent of root is refused", filepath.Dir(root), false},
		{"unrelated absolute path is refused", "/completely/unrelated/path", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPathInside(tc.path, root); got != tc.want {
				t.Fatalf("isPathInside(%q, %q) = %v, want %v", tc.path, root, got, tc.want)
			}
		})
	}
}

func TestReadBoundedFileEnforcesTheSizeLimit(t *testing.T) {
	dir := t.TempDir()

	small := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(small, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := readBoundedFile(small, 10)
	if err != nil || string(data) != "hello" {
		t.Fatalf("readBoundedFile(small, 10) = (%q, %v), want (\"hello\", nil)", data, err)
	}

	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(big, []byte("this content is eleven"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedFile(big, 10); err == nil {
		t.Fatal("readBoundedFile did not enforce the size limit")
	}

	dirPath := filepath.Join(dir, "adir")
	if err := os.Mkdir(dirPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedFile(dirPath, 10); err == nil {
		t.Fatal("readBoundedFile did not refuse a non-regular file")
	}

	if _, err := readBoundedFile(filepath.Join(dir, "missing.txt"), 10); err == nil {
		t.Fatal("readBoundedFile did not error on a missing file")
	}
}

func TestSymlinkBelowReportsASymlinkedComponent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real", "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real", "sub", "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A plain descendant with no symlink in its chain.
	plain := filepath.Join(root, "real", "sub", "file.txt")
	symlinked, err := symlinkBelow(plain, root)
	if err != nil || symlinked {
		t.Fatalf("symlinkBelow(plain descendant) = (%v, %v), want (false, nil)", symlinked, err)
	}

	// A path whose middle component is a symlink into another directory.
	outside := t.TempDir()
	linkPath := filepath.Join(root, "link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Fatal(err)
	}
	viaSymlink := filepath.Join(linkPath, "file.txt")
	symlinked, err = symlinkBelow(viaSymlink, root)
	if err != nil || !symlinked {
		t.Fatalf("symlinkBelow(via symlinked component) = (%v, %v), want (true, nil)", symlinked, err)
	}

	// A path escaping root entirely errors rather than silently reporting false.
	if _, err := symlinkBelow(filepath.Join(outside, "file.txt"), root); err == nil {
		t.Fatal("symlinkBelow did not report a path outside root as an error")
	}
}

func TestCanonicalPublicPathResolvesSymlinksAndTolerantOfMissingTail(t *testing.T) {
	root := t.TempDir()
	realPath := filepath.Join(root, "real")
	if err := os.Mkdir(realPath, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realPath, link); err != nil {
		t.Fatal(err)
	}

	// A symlinked directory resolves to its real target.
	resolved, err := canonicalPublicPath(link)
	if err != nil {
		t.Fatalf("canonicalPublicPath(existing symlink): %v", err)
	}
	wantReal, err := filepath.EvalSymlinks(realPath)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Clean(wantReal) {
		t.Fatalf("canonicalPublicPath(%q) = %q, want %q", link, resolved, wantReal)
	}

	// A path with a not-yet-created tail still resolves through the
	// symlinked ancestor, appending the missing components.
	resolved, err = canonicalPublicPath(filepath.Join(link, "not-yet-created", "leaf.txt"))
	if err != nil {
		t.Fatalf("canonicalPublicPath(missing tail through symlink): %v", err)
	}
	want := filepath.Join(wantReal, "not-yet-created", "leaf.txt")
	if resolved != filepath.Clean(want) {
		t.Fatalf("canonicalPublicPath(missing tail) = %q, want %q", resolved, want)
	}
}

func TestHarvesterWritePublicFileRefusesPathOutsidePublicNamespace(t *testing.T) {
	setHarvestTestJail(t)
	cacheDir := t.TempDir()
	h := mustNew(t, Options{CacheDir: cacheDir})

	// A path inside the public namespace is written successfully.
	publicRoot, err := h.publicRoot()
	if err != nil {
		t.Fatal(err)
	}
	insidePath := filepath.Join(publicRoot, "artifact.txt")
	if err := h.writePublicFile(insidePath, []byte("public content")); err != nil {
		t.Fatalf("writePublicFile(inside public namespace) error: %v", err)
	}
	got, err := os.ReadFile(insidePath)
	if err != nil || string(got) != "public content" {
		t.Fatalf("writePublicFile did not persist the expected content: data=%q err=%v", got, err)
	}

	// A path outside the public namespace (directly under the cache root,
	// or anywhere else) must be refused, never silently redirected.
	outsidePath := filepath.Join(cacheDir, "escaped.txt")
	if err := h.writePublicFile(outsidePath, []byte("must not land here")); err == nil {
		t.Fatal("writePublicFile did not refuse a path outside the public namespace")
	}
	if _, err := os.Stat(outsidePath); !os.IsNotExist(err) {
		t.Fatalf("writePublicFile leaked a file outside the public namespace: stat err=%v", err)
	}

	// A path escaping through a symlinked component under the public root
	// is refused too.
	outsideDir := t.TempDir()
	linkPath := filepath.Join(publicRoot, "escape-link")
	if err := os.Symlink(outsideDir, linkPath); err != nil {
		t.Fatal(err)
	}
	viaLink := filepath.Join(linkPath, "leak.txt")
	if err := h.writePublicFile(viaLink, []byte("must not land here either")); err == nil {
		t.Fatal("writePublicFile did not refuse a symlinked escape under the public namespace")
	}
	entries, err := os.ReadDir(outsideDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "leak") {
			t.Fatalf("writePublicFile wrote through the symlinked escape: %v", entries)
		}
	}
}
