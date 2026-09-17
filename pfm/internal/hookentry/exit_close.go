package hookentry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/kill"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/reload"
)

var (
	exitCloseTerminals = kill.CloseTerminals
	exitCloseEnv       = os.Getenv
	exitCloseInFlight  = reload.InFlight
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
	socketPath, _, found := strings.Cut(exitCloseEnv("TMUX"), ",")
	if !found || socketPath == "" {
		return 0
	}
	if _, fleet := pfmengine.FromSocket(filepath.Base(socketPath)); !fleet {
		return 0
	}
	pane := exitCloseEnv("TMUX_PANE")
	if pane == "" {
		fmt.Fprintln(
			stderr,
			"pfm internal exit-close: left open — TMUX_PANE unset, cannot tell a reload's /exit from a human's",
		)
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
