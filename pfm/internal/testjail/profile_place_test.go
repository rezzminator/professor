package testjail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPackageLabel(t *testing.T) {
	mod := modulePrefix()
	cases := []struct{ name, wd, root, importPath, arg0, want string }{
		{
			"a package directory under the module root",
			"/work/pfm/internal/pricing",
			"/work/pfm",
			"",
			"x.test",
			"internal_pricing",
		},
		{"a nested package directory", "/work/pfm/cmd/pfm", "/work/pfm", "", "x.test", "cmd_pfm"},
		{"the module root falls to the import path", "/work/pfm", "/work/pfm", mod + "e2e.test", "x.test", "e2e"},
		{
			"no module falls to the import path",
			"/tmp/x",
			"",
			mod + "internal/pricing.test",
			"x.test",
			"internal_pricing",
		},
		{
			"a foreign import path falls to the binary name",
			"/tmp/x",
			"",
			"example.com/other.test",
			"/tmp/go-build/pricing.test",
			"pricing",
		},
		{"no build info falls to the binary name", "/tmp/x", "", "", "/tmp/go-build/pricing.test", "pricing"},
		{
			"no working directory falls to the import path",
			"",
			"",
			mod + "internal/pfmdb.test",
			"x.test",
			"internal_pfmdb",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := packageLabel(tc.wd, tc.root, tc.importPath, tc.arg0); got != tc.want {
				t.Fatalf(
					"packageLabel(%q, %q, %q, %q) = %q, want %q",
					tc.wd,
					tc.root,
					tc.importPath,
					tc.arg0,
					got,
					tc.want,
				)
			}
		})
	}
}

func TestModuleRootOfFindsTheNearestGoMod(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "internal", "pricing")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := moduleRootOf(nested); got != "" {
		t.Fatalf("moduleRootOf before any go.mod exists = %q, want none", got)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, nested} {
		if got := moduleRootOf(dir); got != root {
			t.Fatalf("moduleRootOf(%s) = %q, want %q", dir, got, root)
		}
	}
}
