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
	// Home is the reader's home directory, so the dossier can write a path as
	// ~/…; rendering never reads the environment itself.
	Home string
	// ProjectDirs maps each project label to its root directory, so the
	// picker can file every row under the place it belongs to (places.go).
	ProjectDirs map[string]string
	// DirExists answers whether an `@` path names a real directory, and
	// errs when it could not look; nil refuses every typed path. It is the
	// picker's one filesystem door.
	DirExists        func(string) (bool, error)
	Cache1H          bool
	Cache1HByAccount map[int]bool
	// LastLaunch is what the last new-chat launch chose; the picker's new-chat
	// steps open on it (launchwizard.go). Zero when nothing was launched yet.
	LastLaunch fleetdb.LaunchMemory
	// LaunchMemoryError is a failed read of LastLaunch, shown on the ✦ row so
	// an unreadable memory never reads as "nothing launched yet".
	LaunchMemoryError string
	NowNS             int64
	Width             int
	Height            int
	InitialQuery      string
	InitialCursorID   string
	// ScheduleError is a failed read of the unseen-schedule flags, shown in
	// the Chats header so a broken read never renders as "no schedules".
	ScheduleError string
	// FactsError is the first failure met reading the rows' model, effort and
	// working state (internal/rowfacts), shown in the header: rows with no
	// facts must read as "could not look", never as "nothing to show".
	FactsError string
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
	// CosmosEvents are the comms events Cosmos was built from. applyRefresh
	// rebuilds the graph from them when it filters or overrides the snapshot's
	// rows (a deactivated socket, a kill the store has not caught up with), so
	// the sky never draws a chat the Chats tab no longer lists.
	CosmosEvents  []fleetdb.CommsEvent
	CosmosSampler CosmosSampler
	// RefreshFailure marks a snapshot that carries no fleet: the refresh pass
	// failed. applyRefresh keeps the rows it has and shows them as stale, with
	// this error and its age, until a snapshot without it lands.
	RefreshFailure *RefreshFailure
	NoSky          bool
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

// RefreshFailure is why a refresh pass produced no fleet: the stage that
// failed, its error text and the scan clock (unix ns) it failed at; 0 leaves the
// date to the model's own clock.
type RefreshFailure struct {
	Stage string
	Err   string
	AtNS  int64
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
