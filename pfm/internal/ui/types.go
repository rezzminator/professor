package ui

import (
	"context"
	"io"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	pfmstats "github.com/rezzminator/professor/pfm/internal/stats"
)

// Tab is one top-level picker body. Chats preserves the original picker and
// remains the default; Stats and Limits are sampled only while one of them is
// focused.
type Tab uint8

const (
	TabChats Tab = iota
	TabStats
	TabLimits
	TabCosmos
	tabCount
)

type StatsSubtab uint8

const (
	StatsChats StatsSubtab = iota
	StatsDocker
	statsSubtabCount
)

type StatsFocus uint8

const (
	StatsFocusTop StatsFocus = iota
	StatsFocusSubtab
	StatsFocusContent
)

type StatsSort uint8

const (
	StatsSortCPU StatsSort = iota
	StatsSortRAM
)

// StatsSampler is injected so model tests can prove that boot and the Chats
// tab perform zero sampling. The real implementation reads proc/cgroup for
// resource counters and caches one identity lookup per new Docker cgroup ID.
type StatsSampler interface {
	Sample([]compose.Row) (pfmstats.Snapshot, error)
}

// CosmosSampler keeps the open cosmos tab current without making rendering
// query the shared database.
type CosmosSampler interface {
	Sample(ctx context.Context, sinceNS int64) ([]fleetdb.CommsEvent, error)
}

// Picker is the common boundary used by the interactive, plain, and TSV
// frontends. Interactive effects are described by Outcome; implementations do
// not mutate fleet state themselves.
type Picker interface {
	Pick(ctx context.Context, snapshot Snapshot) (Outcome, error)
}

// Snapshot is all state needed for a frame. Rendering never probes the
// filesystem, processes, tmux, or the clock.
type Snapshot struct {
	Rows                   []compose.Row
	View                   compose.View
	KilledCount            int
	SuppressedCount        int
	Refreshing             bool
	PrimaryAccount         int
	AccountIDs             []int
	AccountEmojis          map[int]string
	CodexPrimaryAccount    int
	CodexAccountIDs        []int
	CodexAccountEmojis     map[int]string
	OpenCodePrimaryAccount int
	OpenCodeAccountIDs     []int
	Theme                  string
	Cache1H                bool
	NowNS                  int64
	Width                  int
	Height                 int
	InitialQuery           string
	InitialCursorID        string
	// MergeNewChat is used only by the interactive picker. Plain and TSV
	// output leave it false so their existing two-row output remains stable.
	MergeNewChat bool
	// ApplyKill performs a ⌃X the instant it is typed — the store write, and
	// the kill when the row is live. It is deliberately NOT deferred to quit:
	// the only exits that ever applied a batched kill were the ones that
	// launched something (Enter, ⌃O), so closing the picker the natural
	// way threw every mark away. Nil leaves the picker read-only (the plain
	// twin, tests).
	ApplyKill func(KillChange) error
	// ApplyDeactivate ends one selected live chat while the picker remains open.
	// Nil makes the action unavailable; the model reports that refusal in-frame.
	ApplyDeactivate func(compose.Row) error
	StatsSampler    StatsSampler
	// SamplingContext bounds provider workers to the interactive picker's life.
	SamplingContext context.Context
	Cosmos          compose.CosmosGraph
	CosmosSampler   CosmosSampler
	NoSky           bool
	// CosmosSafe renders the cosmos tab in vscode-safe mode — a slower clock
	// and coarser colour quantisation; resolved by resolveCosmosSafe in
	// cmd/pfm from the --safe flag and TERM_PROGRAM.
	CosmosSafe bool
	// Activity is the presence clock the background refresh reads to pick its
	// cadence. Nil for every non-interactive picker, which reads as always
	// active. Set once when the model is built; refresh snapshots leave it
	// alone, so a rebuild from the stream can never blind the loop to the user.
	Activity *ActivityClock
}

// OutcomeKind identifies why an interactive picker stopped.
type OutcomeKind uint8

const (
	OutcomeNone OutcomeKind = iota
	OutcomeSelected
	OutcomeReboot
	OutcomeDeactivate
	OutcomeCancelled
	OutcomeProfessorUpdate
)

// KillChange is one hidden-state change, applied the moment it is typed.
type KillChange struct {
	ID     string
	Engine pfmengine.ID
	Killed bool
	// Socket and PaneID are the row's live tmux address, when it has one.
	// Hiding a live chat ALSO ends it — kill.Manager runs the same exit
	// choreography ⌃X and `pfm chat kill` share — so the applier forwards
	// both straight through as the kill request's live address. Live mirrors
	// Socket for the receipt; Name identifies it.
	Socket string
	PaneID string
	Live   bool
	Name   string
}

// Outcome contains the selected action plus every in-memory modifier. The
// caller owns persistence and action execution after the TUI has restored the
// terminal.
type Outcome struct {
	Kind                 OutcomeKind
	Row                  compose.Row
	Engine               pfmengine.ID
	PrimaryAccount       int
	ClaudePrimaryAccount int
	Cache1H              bool
	Query                string
	KillChanges          []KillChange
}

// RefreshMsg replaces the cached compose snapshot without doing I/O in Model.
type RefreshMsg struct {
	Snapshot Snapshot
}

// PlainPicker renders a human-readable noninteractive twin.
type PlainPicker struct {
	Writer io.Writer
}

// TSVPicker renders the stable machine-readable twin.
type TSVPicker struct {
	Writer io.Writer
}

// ReadWriteCloser is the narrow terminal handle BubblePicker needs.
type ReadWriteCloser interface {
	io.Reader
	io.Writer
	io.Closer
}

// BubblePicker runs the fancy picker exclusively on /dev/tty. OpenTTY is
// injectable for scratch-terminal tests; Updates may deliver fresh snapshots.
type BubblePicker struct {
	OpenTTY func() (ReadWriteCloser, error)
	Updates <-chan Snapshot
}
