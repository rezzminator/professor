package hookentry

import (
	"fmt"
	"io"
	"os"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// CodexLaunch keeps older sourced shims and rendered tmux commands compatible.
func CodexLaunch(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: pfm internal codex-launch BINARY [arguments...]")
		return 2
	}
	binary, err := obs.Runner(deps.RealRunner{}).LookPath(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "resolve Codex launcher: %v\n", err)
		return 1
	}
	if err := LaunchExec(binary, append([]string{binary}, args[1:]...), os.Environ()); err != nil {
		fmt.Fprintf(stderr, "launch Codex: %v\n", err)
		return 1
	}
	return 0
}
