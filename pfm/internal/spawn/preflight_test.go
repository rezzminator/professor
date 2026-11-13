package spawn

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A binary that resolves nowhere, by name on PATH or by absolute path, is refused before any tmux server exists.
func TestNewSessionRefusesAnUnreachableBinaryBeforeCreatingAServer(t *testing.T) {
	for _, row := range []struct{ name, binary, want string }{
		{"a name on no PATH", "pfm-test-missing-engine-xyz", "engine binary \"pfm-test-missing-engine-xyz\" is not reachable from this process's PATH: " + testjail.NotOnPath("pfm-test-missing-engine-xyz") + " — a systemd user service starts with systemd's default PATH; pin an absolute <engine>.binary in the machine config or extend the unit's Environment=PATH"},
		{"a missing absolute path", "{dir}/engines/claude", "engine binary {dir}/engines/claude is not on disk: stat {dir}/engines/claude: no such file or directory — fix the configured <engine>.binary path"},
	} {
		t.Run(row.name, func(t *testing.T) {
			dir := t.TempDir()
			binary := strings.ReplaceAll(row.binary, "{dir}", dir)
			err := (TmuxSpawner{TmuxDir: dir}).NewSession(context.Background(), SessionSpec{
				Socket:  "preflight",
				Session: "preflight",
				Window:  "w",
				CWD:     dir,
				Run:     binary,
				Binary:  binary,
				Width:   80,
				Height:  24,
			})
			entries, readErr := os.ReadDir(dir)
			if got := strings.ReplaceAll(
				fmt.Sprint(err),
				dir,
				"{dir}",
			); got != row.want || readErr != nil ||
				len(entries) != 0 {
				t.Fatalf(
					"NewSession error = %q, want %q (socket dir entries %d, %v)",
					got,
					row.want,
					len(entries),
					readErr,
				)
			}
		})
	}
}
