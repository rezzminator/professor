package testjail

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// HungTmux puts a fake `tmux` first on PATH whose server never answers a
// list-panes: it hangs past gather.ProbeTimeout, then exits 1 with no words, the
// way a stopped or wedged server leaves a client that was waiting on it.
// kill-server exits killServerRC; every other subcommand fails. It returns the
// fake's path for a caller that takes an explicit binary.
func HungTmux(t *testing.T, killServerRC int) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "tmux")
	script := fmt.Sprintf(`#!/bin/sh
for argument do
	case "$argument" in
	list-panes) sleep 5; exit 1 ;;
	kill-server) exit %d ;;
	esac
done
echo "hung tmux fake: unexpected $*" >&2
exit 1
`, killServerRC)
	if err := WriteExecutable(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write hung tmux fake: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+paths.OSEnv{}.Get("PATH"))
	return binary
}
