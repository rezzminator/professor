package gather

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// UnixSockets lists every bound path in a jail's net/unix, past the header; an
// unbound socket has no path field.
func TestUnixSocketsReadsAJailTable(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "net"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeProcFixture(t, filepath.Join(root, "net", "unix"),
		"Num RefCount Protocol Flags Type St Inode Path\n"+
			"0: 2 0 00010000 0001 01 1 /tmp/tmux-1000/cc-live\n"+
			"1: 3 0 00000000 0001 03 2\n"+
			"2: 2 0 00010000 0001 01 3 @/tmp/.X11-unix/X0\n")
	got, err := UnixSockets(root)
	if want := []string{"/tmp/tmux-1000/cc-live", "@/tmp/.X11-unix/X0"}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("UnixSockets(%s) = %q, %v; want %q", root, got, err, want)
	}
}
