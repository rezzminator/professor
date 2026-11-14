//go:build jail

package gather

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

// TestNativeProcFSSmokeOwnProcessOnly drives whichever reader this platform
// actually uses — /proc on Linux, sysctl and lsof on macOS — against the one
// process every platform lets a caller read in full: its own. Asking for the
// native reader rather than naming RealProcFS is the point; it is what makes
// this a contract both implementations must satisfy.
func TestNativeProcFSSmokeOwnProcessOnly(t *testing.T) {
	proc := NewProcFS("")
	pid := os.Getpid()

	cmdline, err := proc.Cmdline(pid)
	if err != nil {
		t.Fatalf("Cmdline(own pid) error = %v", err)
	}
	if len(cmdline) == 0 {
		t.Fatal("Cmdline(own pid) is empty")
	}
	if _, err := proc.Environ(pid); err != nil {
		t.Fatalf("Environ(own pid) error = %v", err)
	}
	if _, err := proc.FDLinks(pid); err != nil {
		t.Fatalf("FDLinks(own pid) error = %v", err)
	}
	stat, err := proc.Stat(pid)
	if err != nil {
		t.Fatalf("Stat(own pid) error = %v", err)
	}
	if stat.ParentPID <= 0 || stat.StartTime == 0 {
		t.Fatalf("Stat(own pid) = %+v, want parent and start time", stat)
	}
	birther, ok := proc.(ProcBirth)
	if !ok {
		t.Fatal("native ProcFS does not report a birth time")
	}
	birth, err := birther.Birth(pid)
	if err != nil {
		t.Fatalf("Birth(own pid) error = %v", err)
	}
	if birth <= 0 || birth > time.Now().Unix() {
		t.Fatalf("Birth(own pid) = %d, want a past epoch second", birth)
	}
	memory, ok := proc.(ProcMemory)
	if !ok {
		t.Fatal("native ProcFS does not report resident memory")
	}
	resident, err := memory.RSSKB(pid)
	if err != nil {
		t.Fatalf("RSSKB(own pid) error = %v", err)
	}
	if resident <= 0 {
		t.Fatalf("RSSKB(own pid) = %d, want a positive size", resident)
	}
}
