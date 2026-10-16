// Package themesync is `pfm theme`: one pfm-owned palette, rendered into the
// native format of every surface the user looks at — Claude Code's custom
// theme files, pfm's per-chat tmux servers, Herdr's config, Ghostty's config
// and the pfm VS Code extension — applied live, read back from each surface,
// and stamped with one generation number so "in sync" is a fact the status
// matrix proves rather than a claim. Nothing here runs until `pfm theme set`
// writes the state file: every door other packages call is a no-op without it.
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

// Mode is what the user asked for: follow macOS, or hold one side.
type Mode string

const (
	Dark      Appearance = "dark"
	Light     Appearance = "light"
	ModeAuto  Mode       = "auto"
	ModeDark  Mode       = "dark"
	ModeLight Mode       = "light"
)

// StateColors are the four agent states, one hex each, identical on every
// surface that draws a state.
type StateColors struct {
	Blocked string `json:"blocked"`
	Working string `json:"working"`
	Done    string `json:"done"`
	Idle    string `json:"idle"`
}

// Variant is one side (dark or light) of a palette: the terminal colours
// (ANSI 0–15, background, foreground, cursor, selection), the UI roles the
// non-terminal surfaces draw with, and the state colours.
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
	ANSI                [16]string  `json:"ansi"`
	State               StateColors `json:"state"`
}

// Palette is one theme: both variants plus the Herdr built-in names its
// custom tokens layer onto.
type Palette struct {
	Name       string
	Title      string
	Dark       Variant
	Light      Variant
	HerdrDark  string
	HerdrLight string
}

// Variant returns the side a resolved appearance selects.
func (palette Palette) Variant(appearance Appearance) Variant {
	if appearance == Light {
		return palette.Light
	}
	return palette.Dark
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

// Report is one set/sync/status/off run.
type Report struct {
	Enabled    bool            `json:"enabled"`
	Palette    string          `json:"palette"`
	Mode       Mode            `json:"mode"`
	Appearance Appearance      `json:"appearance"`
	Generation int             `json:"generation"`
	Surfaces   []SurfaceResult `json:"surfaces"`
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
	// PfmBinary is the pfm the appearance follower's launch agent runs.
	PfmBinary string
	// Runner runs defaults, herdr, ghostty, ps, kill and launchctl.
	Runner deps.Runner
	// Tmux reaches pfm's chat servers.
	Tmux TmuxServers
	// systemApps is the system Applications directory ("" is /Applications);
	// tests point it into their own tree.
	systemApps string
}

// Request is `pfm theme set`.
type Request struct {
	Palette string
	Mode    Mode
}

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

// SyncOn reports whether a `pfm theme set` is in force. A state file that
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
