package chat

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestKillServerRefusesNativeSocket(t *testing.T) {
	root := t.TempDir()
	socket := filepath.Join(root, "herdr.sock")
	if err := os.WriteFile(socket, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := KillServer(
		context.Background(),
		paths.Values{TmuxDir: root, StateDB: filepath.Join(root, "state.db")},
		socket,
		"%0",
	)
	body, readErr := os.ReadFile(socket)
	if err == nil || err.Error() != "KillServer requires a tmux socket name; use EndResolved for a native Herdr seat" ||
		readErr != nil ||
		string(body) != "preserve" {
		t.Fatalf("native end err=%v body=%q read=%v", err, body, readErr)
	}
}
