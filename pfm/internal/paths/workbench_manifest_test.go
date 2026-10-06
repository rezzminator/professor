package paths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestHasWorkbenchManifest(t *testing.T) {
	for _, tc := range []struct {
		name      string
		setup     func(*testing.T, string) string
		want      bool
		wantError bool
	}{
		{name: "nothing there"},
		{name: ".professor without manifest", setup: func(t *testing.T, dir string) string {
			if err := os.Mkdir(filepath.Join(dir, ".professor"), 0o700); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{name: ".professor is a file", setup: func(t *testing.T, dir string) string {
			if err := os.WriteFile(filepath.Join(dir, ".professor"), []byte("notes\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{name: ".professor is a symlink", setup: func(t *testing.T, dir string) string {
			other := filepath.Join(t.TempDir(), ".professor")
			if err := os.Mkdir(other, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(other, "workbench.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, filepath.Join(dir, ".professor")); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{name: "manifest is a symlink", setup: func(t *testing.T, dir string) string {
			if err := os.Mkdir(filepath.Join(dir, ".professor"), 0o700); err != nil {
				t.Fatal(err)
			}
			manifest := filepath.Join(dir, "real.json")
			if err := os.WriteFile(manifest, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(manifest, WorkbenchManifest(dir)); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{name: "real manifest", want: true, setup: func(t *testing.T, dir string) string {
			if err := os.Mkdir(filepath.Join(dir, ".professor"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(WorkbenchManifest(dir), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{name: "Lstat fails", wantError: true, setup: func(_ *testing.T, dir string) string {
			return filepath.Join(dir, strings.Repeat("x", 300))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.setup != nil {
				dir = tc.setup(t, dir)
			}
			got, err := HasWorkbenchManifest(dir)
			if tc.wantError {
				if got || !errors.Is(err, syscall.ENAMETOOLONG) ||
					!strings.HasPrefix(err.Error(), "inspect workbench "+filepath.Join(dir, ".professor")+": ") {
					t.Fatalf("HasWorkbenchManifest(%q) = %v, %v, want named ENAMETOOLONG", dir, got, err)
				}
			} else if got != tc.want || err != nil {
				t.Fatalf("HasWorkbenchManifest(%q) = %v, %v, want %v, nil", dir, got, err, tc.want)
			}
		})
	}
}
