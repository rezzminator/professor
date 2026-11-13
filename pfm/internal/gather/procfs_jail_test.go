//go:build jail

package gather

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// Image names the file a process EXECUTES by device and inode — the identity
// an install's rename-over changes and a running process keeps.
func TestRealProcFSImageIsTheExecutablesFileID(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "pfm")
	if err := testjail.WriteExecutable(binary, []byte("image"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "proc", "7"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(binary, filepath.Join(root, "proc", "7", "exe")); err != nil {
		t.Fatal(err)
	}
	table, ok := NewProcFS(filepath.Join(root, "proc")).(ProcImage)
	if !ok {
		t.Fatal("RealProcFS does not report process images")
	}
	got, err := table.Image(7)
	if err != nil {
		t.Fatal(err)
	}
	want, err := FileIDOf(binary)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || want.Inode == 0 {
		t.Fatalf("Image = %+v, want %+v", got, want)
	}
}
