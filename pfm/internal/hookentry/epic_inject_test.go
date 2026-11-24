package hookentry

import (
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestEpicInjectNativeWindowNameIsTheSeatName holds I1: a native Herdr chat's
// window name is its seat name, read without a tmux call against the Herdr
// socket.
func TestEpicInjectNativeWindowNameIsTheSeatName(t *testing.T) {
	identity := resolve.Identity{
		Backend:    "herdr",
		SocketPath: filepath.Join(t.TempDir(), "herdr.sock"),
		Pane:       "w1:p2",
		Session:    "E_alpha_native",
	}
	window, err := epicInjectWindowName(testwait.Context(t), identity)
	if err != nil || window != "E_alpha_native" {
		t.Fatalf("window name = %q, %v; want the seat name %q and no error", window, err, "E_alpha_native")
	}
}
