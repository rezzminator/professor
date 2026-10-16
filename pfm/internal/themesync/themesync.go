// Package themesync is `pfm theme`: Claude Code's `theme` setting is the one
// read-only source, derived into a Variant (or, for `auto`, two) and rendered
// into the native format of every surface the user looks at — pfm's per-chat
// tmux servers, Herdr's config and the pfm VS Code extension — applied live,
// read back from each surface and stamped with one generation number so "in
// sync" is a fact the status matrix proves rather than a claim. No Claude file
// is ever written. Nothing here runs until `pfm theme on` writes the state
// file: every door other packages call is a no-op without it.
package themesync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// Appearance is the resolved light/dark side a render uses.
type Appearance string

const (
	Dark  Appearance = "dark"
	Light Appearance = "light"
)

// StateColors are the four agent states, one hex each, identical on every
// surface that draws a state.
type StateColors struct {
	Blocked string `json:"blocked"`
	Working string `json:"working"`
	Done    string `json:"done"`
	Idle    string `json:"idle"`
}

// Variant is one side of a derived theme: the terminal colours (ANSI 0–15,
// background, foreground, cursor, selection), the UI roles the non-terminal
// surfaces draw with, and the state colours.
type Variant struct {
	Background          string      `json:"background"`
	Foreground          string      `json:"foreground"`
	Cursor              string      `json:"cursor"`
	SelectionBackground string      `json:"selectionBackground"`
	SelectionForeground string      `json:"selectionForeground"`
	Surface             string      `json:"surface"`
	Overlay             string      `json:"overlay"`
	Muted               string      `json:"muted"`
	Accent              string      `json:"accent"`
	Orange              string      `json:"orange"`
	ANSI                [16]string  `json:"ansi"`
	State               StateColors `json:"state"`
}

// Applied is the derived theme in force: the Claude `theme` value it came
// from and its Variant — for `auto` both sides, the dark one primary. It is
// persisted in state.json so a surface born later (a tmux server at spawn)
// renders it without reading the source.
type Applied struct {
	Value     string     `json:"value"`
	Title     string     `json:"title"`
	Auto      bool       `json:"auto,omitempty"`
	Side      Appearance `json:"side"`
	Primary   Variant    `json:"primary"`
	Alternate *Variant   `json:"alternate,omitempty"`
}

// variant is the side a single-variant surface (tmux, VS Code) draws: the
// light alternate of an auto theme under a light appearance, else the primary.
func (applied Applied) variant(appearance Appearance) Variant {
	if applied.Auto && appearance == Light && applied.Alternate != nil {
		return *applied.Alternate
	}
	return applied.Primary
}

// SourceInfo names the governing Claude account and the theme it set.
type SourceInfo struct {
	Account string     `json:"account"`
	Path    string     `json:"path"`
	Value   string     `json:"value"`
	Title   string     `json:"title"`
	Side    Appearance `json:"side"`
}

// Flip is the measured latency of the last source change: seconds from the
// source file's mtime (At) to each surface's apply, and to the VS Code
// extension's ack when it has acknowledged the generation.
type Flip struct {
	At     string   `json:"at"`
	Source string   `json:"source"`
	Tmux   float64  `json:"tmux,omitempty"`
	Herdr  float64  `json:"herdr,omitempty"`
	VSCode float64  `json:"vscode,omitempty"`
	Ack    *float64 `json:"ack,omitempty"`
}

// Verdict is one surface's row in the sync matrix.
type Verdict string

const (
	VerdictApplied  Verdict = "APPLIED"
	VerdictInSync   Verdict = "IN-SYNC"
	VerdictPending  Verdict = "PENDING"
	VerdictDrift    Verdict = "DRIFT"
	VerdictAbsent   Verdict = "ABSENT"
	VerdictError    Verdict = "ERROR"
	VerdictRestored Verdict = "RESTORED"
	VerdictSource   Verdict = "SOURCE"
)

// SurfaceResult is one surface's outcome: the generation the surface itself
// reads back (0 when it carries none), the file or socket it lives at, and a
// detail that names what was looked for when the verdict is ABSENT or what
// failed when it is ERROR or DRIFT.
type SurfaceResult struct {
	Surface    string  `json:"surface"`
	Verdict    Verdict `json:"verdict"`
	Generation int     `json:"generation"`
	Path       string  `json:"path"`
	Detail     string  `json:"detail"`
}

// Report is one on/sync/status/off run.
type Report struct {
	Enabled    bool        `json:"enabled"`
	Source     *SourceInfo `json:"source,omitempty"`
	Appearance Appearance  `json:"appearance"`
	Generation int         `json:"generation"`
	LastFlip   *Flip       `json:"lastFlip,omitempty"`
	// Changed says this run applied a surface; Servers counts the tmux
	// servers it reached. The quiet follower line is built from them.
	Changed  bool            `json:"-"`
	Servers  int             `json:"-"`
	Surfaces []SurfaceResult `json:"surfaces"`
}

// OK reports whether no surface is in DRIFT or ERROR.
func (report Report) OK() bool {
	for _, surface := range report.Surfaces {
		if surface.Verdict == VerdictDrift || surface.Verdict == VerdictError {
			return false
		}
	}
	return true
}

// TmuxServers is the tmux seam: the live pfm chat servers, one global option
// read, and an option-vector apply in config.ChatServerOptions' shape.
type TmuxServers interface {
	Sockets(ctx context.Context) ([]string, error)
	Show(ctx context.Context, socket, name string) (string, error)
	Apply(ctx context.Context, socket string, options [][]string) error
}

// Env is everything a run touches, injectable for tests.
type Env struct {
	// Home is the user's home directory every surface path derives from.
	Home string
	// StateDir is DefaultStateDir(Home) unless a test points it elsewhere.
	StateDir string
	// PfmBinary is the pfm the follower's launch agent runs.
	PfmBinary string
	// Runner runs defaults, herdr, ps and launchctl.
	Runner deps.Runner
	// Tmux reaches pfm's chat servers.
	Tmux TmuxServers
	// LaunchdJob is the launchd job label this process runs as (its
	// XPC_SERVICE_NAME), "" outside launchd.
	LaunchdJob string
}

// FollowerLabel is the launchd job label of the follower's launch agent.
const FollowerLabel = followerLabel

// tmuxStateName is the server option the agent-state hook sets and the
// theme's state-colour formats read; off unsets it with the theme's own.
const tmuxStateName = "@pfm-state"

// TmuxStateOption is the option vector the agent-state hook sets on its own
// server, which the theme's state-colour formats read.
func TmuxStateOption(state string) []string {
	return []string{"set-option", "-g", tmuxStateName, state}
}

// TmuxStateClear is the option vector a session end sets: the state unset, so
// a server that outlives its engine shows no agent state.
func TmuxStateClear() []string {
	return []string{"set-option", "-gu", tmuxStateName}
}

// DefaultStateDir is where the sync keeps its state, contract and backups.
func DefaultStateDir(home string) string {
	return filepath.Join(home, ".local", "state", "pfm", "theme")
}

// StateFileName is the sync's state file inside the state dir.
const StateFileName = "state.json"

// SyncOn reports whether a `pfm theme on` is in force. A state file that
// exists but cannot be read or parsed reports enabled, so a door errs on
// the side of running and surfacing the error rather than silently skipping.
func SyncOn(stateDir string) bool {
	content, err := os.ReadFile(filepath.Join(stateDir, StateFileName))
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	var state struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(content, &state); err != nil {
		return true // load surfaces the parse error wherever a door actually runs
	}
	return state.Enabled
}

// RestorePending reports whether a `pfm theme off` still has work: the sync is
// on, an earlier off marked itself pending, or a user file the sync edited
// still holds its snapshot because that off failed to restore it (Off clears
// Enabled before it restores).
// Like SyncOn, a state file that cannot be read or parsed reports pending.
func RestorePending(stateDir string) bool {
	state, found, err := stateStore{dir: stateDir}.load()
	if err != nil {
		return true // the off this gates surfaces the error
	}
	return found && (state.Enabled || state.OffPending || state.Herdr != nil || len(state.VSCode) > 0)
}
