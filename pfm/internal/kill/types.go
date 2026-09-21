package kill

import (
	"context"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// SelfEnvironment contains only the caller state used for self-identification.
type SelfEnvironment struct {
	TMUX            string
	TMUXPane        string
	ClaudeSessionID string
}

// Target is one kill identity and its optional live tmux address.
type Target struct {
	Engine     pfmengine.ID
	ID         string
	DataPath   string
	SocketPath string
	SocketName string
	PaneID     string
}

// Request describes a public kill invocation.
type Request struct {
	ID   string
	Self bool
	// Engine names the chat engine when the caller already knows it because it
	// is looking at the row — the picker. It vouches for a chat the index has
	// not caught up with, which is every freshly spawned agent: their
	// transcripts reach the store only after Claude flushes the file. Left
	// empty, an unknown id is still an error, so a mistyped `pfm kill`
	// argument cannot quietly record a kill for nothing.
	Engine pfmengine.ID
	// RolloutPath names the Codex rollout file the caller's own row carries,
	// when known. It lets an unindexed lineage member resolve to its root
	// through the file's own session_meta header (resolveUnindexedCodexParent)
	// instead of hiding under the member's own id — the id the picker's
	// composed row, keyed on the root, never carries. Ignored for Claude ids
	// and whenever the caller does not have a path to offer.
	RolloutPath string
	// SocketName and PaneID carry a live address already resolved by the fleet
	// composer. Codex tool shells have no ambient TMUX, so this is the only way
	// their `kill self --exit` call can name the pane it owns. SocketName stays
	// the immutable tmux socket name; the manager derives its path inside the
	// configured tmux directory and accepts no caller-supplied path.
	SocketName  string
	PaneID      string
	Exit        bool
	Environment SelfEnvironment
}

// ExitArgs are serialized onto the internal kill-exit argv.
type ExitArgs struct {
	Engine     pfmengine.ID
	ID         string
	DataPath   string
	SocketPath string
	SocketName string
	PaneID     string
}

// TmuxClient abstracts all tmux mutation behind a jailed implementation.
type TmuxClient interface {
	PanePID(ctx context.Context, socketPath, paneID string) (int, error)
	// PaneExists distinguishes "could not ask" from "gone": a transient tmux
	// failure returns a non-nil error and an unspecified bool, never a bare
	// false a caller could mistake for a confirmed exit.
	PaneExists(ctx context.Context, socketPath, paneID string) (bool, error)
	SendLine(ctx context.Context, socketPath, paneID, line string) error
	KillPane(ctx context.Context, socketPath, paneID string) error
	KillServer(ctx context.Context, socketPath string) error
	ClientTTYs(ctx context.Context, socketPath string) ([]string, error)
	PanesByTTY(ctx context.Context, socketPath string) (map[string]string, error)
}

// ExitSpawner starts the detached internal finisher.
type ExitSpawner interface {
	Spawn(ctx context.Context, args ExitArgs) error
}

// Refresher updates indexed prompt counts after the engine flushes its file.
type Refresher interface {
	Refresh(ctx context.Context) error
}

// Dependencies replaces process/tmux/time boundaries in tests.
type Dependencies struct {
	Paths        paths.Values
	ClaudeRoots  []string
	CodexHomes   []string
	ConfigPath   string
	ProcFS       gather.ProcFS
	Tmux         TmuxClient
	Spawner      ExitSpawner
	Now          func() time.Time
	Executable   string
	Delay        time.Duration
	PollEvery    time.Duration
	PollAttempts int
	Refresher    Refresher
	// ConfirmEvery and ConfirmAttempts bound Manager.ConfirmExit's own
	// caller-side wait — short, because unlike the finisher's detached grace
	// window this one blocks the interactive/MCP kill call.
	ConfirmEvery    time.Duration
	ConfirmAttempts int
}

// Manager performs public kill operations.
type Manager struct {
	database        *store.Store
	proc            gather.ProcFS
	tmux            TmuxClient
	spawner         ExitSpawner
	now             func() time.Time
	paths           resolvedPaths
	confirmEvery    time.Duration
	confirmAttempts int
}

// Finisher performs the detached graceful-exit phase.
type Finisher struct {
	database     *store.Store
	tmux         TmuxClient
	refresher    Refresher
	now          func() time.Time
	delay        time.Duration
	pollEvery    time.Duration
	pollAttempts int
	paths        resolvedPaths
}

type resolvedPaths struct {
	home       string
	sidDir     string
	codexHomes []string
	tmuxDir    string
}
