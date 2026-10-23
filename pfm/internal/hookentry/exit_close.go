package hookentry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

var (
	exitCloseTerminals = kill.CloseTerminals
	exitCloseIdentify  = func(ctx context.Context) (resolve.Identity, error) {
		identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{})
		if err != nil {
			return resolve.Identity{}, err
		}
		return identifier.Identify(ctx)
	}
	exitCloseInFlight = reload.InFlight
)

// ExitClose closes the terminal viewport after a human-requested Claude exit.
func ExitClose(stdin io.Reader, stderr io.Writer) int {
	var hook struct {
		Event  string `json:"hook_event_name"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(stdin).Decode(&hook); err != nil {
		fmt.Fprintf(stderr, "pfm internal exit-close: decode hook payload (fail-open): %v\n", err)
		return 0
	}
	if hook.Event != "SessionEnd" || hook.Reason != "prompt_input_exit" {
		return 0
	}
	identity, err := exitCloseIdentify(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal exit-close: left open — %v\n", err)
		return 0
	}
	if identity.Backend != spawn.BackendTmux {
		return 0
	}
	socketPath, pane := identity.SocketPath, identity.Pane
	if _, fleet := pfmengine.FromSocket(filepath.Base(socketPath)); !fleet {
		return 0
	}
	resolved, err := paths.Resolve()
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal exit-close: left open — resolve paths (fail-open): %v\n", err)
		return 0
	}
	socketName := filepath.Base(socketPath)
	if inFlight, err := exitCloseInFlight(resolved.SIDDir, socketName, pane); err != nil {
		fmt.Fprintf(stderr, "pfm internal exit-close: left open — probe reload lock (fail-open): %v\n", err)
		return 0
	} else if inFlight {
		fmt.Fprintf(
			stderr,
			"pfm internal exit-close: left open — reload in flight for %s %s: the pane is being rebooted, not closed\n",
			socketName,
			pane,
		)
		return 0
	}
	closed, skipped, err := exitCloseTerminals(context.Background(), socketPath, kill.ViewportDeps{
		Tmux: kill.TmuxKiller{}, Processes: kill.CommandProcessTable{}, Signals: kill.ProcessSignaller{},
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal exit-close: close terminals (fail-open): %v\n", err)
		return 0
	}
	for _, reason := range skipped {
		fmt.Fprintf(stderr, "pfm internal exit-close: left open — %s\n", reason)
	}
	if len(closed) > 0 {
		fmt.Fprintf(stderr, "pfm internal exit-close: closed %d terminal(s) %v\n", len(closed), closed)
	}
	return 0
}
