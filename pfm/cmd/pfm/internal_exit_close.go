package main

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

// exitCloseTerminals is the closer, as a package var so a test can assert the
// socket this hook derived from its environment without signalling a real
// shell.
var exitCloseTerminals = kill.CloseTerminals

// exitCloseEnv reads the environment, swappable for the same reason.
var exitCloseEnv = os.Getenv

// exitCloseInFlight asks reload whether this pane's mutex is held right now.
var exitCloseInFlight = reload.InFlight

// runExitClose is the SessionEnd hook body for `pfm internal exit-close`. It
// closes the TERMINAL a chat was being watched through when the human ends the
// chat with /exit.
//
// The chat pane needs no help: a fleet seat is exec'd, so the engine IS the
// pane root and the pane dies with it. What survives is one layer further out
// — the shell that SPAWNED `tmux attach` as a child (a VS Code tab's zsh) and
// returns to a prompt the moment the chat server dies. That prompt is the tab
// that stays open, and closing it is this hook's whole job.
//
// Claude Code's SessionEnd contract: /exit reports reason "prompt_input_exit",
// /clear reports "clear", and a logout reports "logout". Only the first ends
// the chat at the human's request, so only the first closes anything —
// "clear" in particular MUST fall through, because that chat keeps running and
// closing its terminal would take down a live session.
//
// Fail-open by construction: every path returns 0. A hook that can refuse a
// session's exit is worse than a terminal left open.
func runExitClose(stdin io.Reader, stderr io.Writer) int {
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

	// $TMUX is "socket,serverpid,sessionid". Absent means this engine is not
	// running in tmux at all — a bare terminal has no viewport to close.
	socketPath, _, found := strings.Cut(exitCloseEnv("TMUX"), ",")
	if !found || socketPath == "" {
		return 0
	}
	// The prefix gate is what keeps this hook off a human's own tmux. The same
	// settings file arms it for EVERY Claude session on the host, so a socket
	// that is not a fleet chat server is left strictly alone.
	if _, fleet := pfmengine.FromSocket(filepath.Base(socketPath)); !fleet {
		return 0
	}
	// A reload ends the old process with this same /exit and reboots the
	// pane in place, holding its pane mutex throughout. Closing the terminal
	// then takes the tab down mid-reboot, leaving the reborn chat alive in
	// tmux with nobody watching it — so an in-flight reload leaves every
	// terminal alone, and anything that stops this probe from answering
	// leaves them alone too (fail-open is "tab stays").
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

	closed, skipped, err := exitCloseTerminals(
		context.Background(),
		socketPath,
		kill.ViewportDeps{
			Tmux:      kill.CommandTmux{},
			Processes: kill.CommandProcessTable{},
			Signals:   kill.ProcessSignaller{},
		},
	)
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
