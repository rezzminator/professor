package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestHasWorkbenchManifest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, string) string
		want  string
	}{
		{name: "nothing there", want: "false <nil>"},
		{name: ".professor without manifest", want: "false <nil>", setup: func(t *testing.T, dir string) string {
			if err := os.Mkdir(filepath.Join(dir, ".professor"), 0o700); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{name: ".professor is a file", want: "false <nil>", setup: func(t *testing.T, dir string) string {
			if err := os.WriteFile(filepath.Join(dir, ".professor"), []byte("notes\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{name: ".professor is a symlink", want: "false <nil>", setup: func(t *testing.T, dir string) string {
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
		{name: "manifest is a symlink", want: "false <nil>", setup: func(t *testing.T, dir string) string {
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
		{name: "directory marker", want: "false inspect workbench {dir}/.professor/workbench.json: not a regular file", setup: func(t *testing.T, dir string) string {
			if err := os.MkdirAll(WorkbenchManifest(dir), 0o700); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{name: "FIFO marker", want: "false inspect workbench {dir}/.professor/workbench.json: not a regular file", setup: func(t *testing.T, dir string) string {
			if err := os.Mkdir(filepath.Join(dir, ".professor"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(WorkbenchManifest(dir), 0o600); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{name: "real manifest", want: "true <nil>", setup: func(t *testing.T, dir string) string {
			if err := os.Mkdir(filepath.Join(dir, ".professor"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(WorkbenchManifest(dir), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{name: "Lstat fails", want: "false inspect workbench {dir}/.professor: lstat {dir}/.professor: file name too long", setup: func(_ *testing.T, dir string) string {
			return filepath.Join(dir, strings.Repeat("x", 300))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.setup != nil {
				dir = tc.setup(t, dir)
			}
			got, err := HasWorkbenchManifest(dir)
			if text := strings.ReplaceAll(fmt.Sprintf("%t %v", got, err), dir, "{dir}"); text != tc.want {
				t.Fatalf("HasWorkbenchManifest = %q, want %q", text, tc.want)
			}
		})
	}
}
