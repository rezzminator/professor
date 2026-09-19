package compose

import (
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/store"
)

// Kind identifies the action and visual treatment for a row.
type Kind uint8

const (
	LiveClaude Kind = iota + 1
	LiveCodex
	LiveSplit
	Agent
	ResumeClaude
	ResumeCodex
	NewClaude
	NewCodex
	// Booting is a chat still sitting at a startup prompt (folder trust, MCP
	// approval): its pane and process are live but its statusline has not
	// written a SID crumb yet, so no transcript identity exists to key an
	// ordinary live row on. Not killable, not resumable — Enter is the only
	// live operation it supports (attach), matching the socket-only identity
	// it carries.
	Booting
	// ResumeOpenCode is one indexed OpenCode session. Appended last ON PURPOSE:
	// Kind values are compared numerically in golden fixtures, so renumbering
	// an existing kind would silently rewrite every fixture row.
	ResumeOpenCode
	// NewOpenCode is appended for the same compatibility reason. It launches a
	// fresh OpenCode TUI in a fleet-owned ox socket.
	NewOpenCode
	// ProfessorUpdate is a cached, interactive-only release notice inserted by
	// cmd/pfm ahead of the merged new-chat row. It is deliberately not emitted
	// by Compose, so plain and TSV output retain their stable row contracts.
	ProfessorUpdate
	// ProfessorUpdateFailed is ProfessorUpdate's failure twin: the detached
	// update checker has been failing on every attempt instead of finding (or
	// ruling out) a release. It gets its own row for the same reason
	// ProfessorUpdate does — a stderr line printed before the interactive
	// picker takes the terminal is gone the instant the alt screen opens, and
	// a swallowed failure must never render identically to "no update
	// available". Never emitted by Compose either, for the same reason.
	ProfessorUpdateFailed
)

func (kind Kind) String() string {
	switch kind {
	case LiveClaude:
		return "live-claude"
	case LiveCodex:
		return "live-codex"
	case LiveSplit:
		return "live-split"
	case Agent:
		return "agent"
	case ResumeClaude:
		return "resume-claude"
	case ResumeCodex:
		return "resume-codex"
	case NewClaude:
		return "new-claude"
	case NewCodex:
		return "new-codex"
	case Booting:
		return "booting"
	case ResumeOpenCode:
		return "resume-opencode"
	case NewOpenCode:
		return "new-opencode"
	case ProfessorUpdate:
		return "professor-update"
	case ProfessorUpdateFailed:
		return "professor-update-failed"
	default:
		return "unknown"
	}
}

// IsLiveSeat reports whether kind is a running primary chat seat.
func (kind Kind) IsLiveSeat() bool {
	return kind == LiveClaude || kind == LiveCodex || kind == LiveSplit
}

// IsAddressable reports whether kind has a live process or pane that chat
// verbs can address.
func (kind Kind) IsAddressable() bool {
	return kind.IsLiveSeat() || kind == Agent || kind == Booting
}

// View selects the default, all, or killed-only row set.
type View uint8

const (
	DefaultView View = iota
	AllView
	KilledView
)

// AccountRoot associates one transcript/config root with its fleet account.
type AccountRoot struct {
	Account int
	Path    string
	// ConfigDir is the seat's config dir (CLAUDE_CONFIG_DIR / CODEX_HOME). A
	// live process names its seat by this, and it stays distinct when every
	// seat's Path resolves to one shared transcript store.
	ConfigDir string
}

// Options controls pure presentation choices.
type Options struct {
	View                View
	CurrentDir          string
	CurrentSocket       string
	PrimaryAccount      int
	CodexAccountIDs     []int
	PrimaryCodexAccount int
	OpenCodeAccountIDs  []int
	PrimaryOpenCode     int
	NowNS               int64
}

// Input is the complete immutable input to one composition pass.
type Input struct {
	Snapshot         gather.Snapshot
	Transcripts      []store.Transcript
	Rollouts         []store.Rollout
	OpenCodeSessions []store.OpenCodeSession
	CxNames          map[string]string
	Killed           []store.Killed
	AccountRoots     []AccountRoot
	CodexHomes       []AccountRoot
	Options          Options
}

// Row is one live, resumable, agent, or new-chat choice.
type Row struct {
	Kind      Kind
	ID        string
	Path      string
	ConfigDir string
	Socket    string
	PaneID    string
	// PanePIDs are the roots of this live socket's process tree. They are
	// presentation-neutral live facts used only by the lazy Stats sampler.
	PanePIDs    []int
	SessionName string
	WindowName  string
	Name        string
	LastPrompt  string
	Project     string
	CWD         string
	Size        int64
	PromptCount int64
	// AssistantCount is set only for a ResumeOpenCode row: an OpenCode
	// session has no file size, so its reality signal is prompts answered,
	// not bytes on disk. A session with prompts but no assistant reply is
	// exactly as empty as a Claude transcript with no visible turns.
	AssistantCount int64
	ActivityNS     int64
	AgeNS          int64
	Account        int
	Accounts       []int
	Killed         bool
	// NameKilled marks a row killed by its "_KILL…" label rather than by a
	// store row: the picker's kill key cannot toggle it, because the label —
	// not the killed table — is what keeps it out of the list.
	NameKilled  bool
	BG          bool
	C1H         bool
	Attached    bool
	Here        bool
	ServerCount int
	SplitCount  int
}

// RowKey is a row's identity for the picker and the cosmos alike — the ONE
// implementation (K3): the chat's ID when it has one, else its socket and
// pane (a split live window has no ID of its own), else where it lives. A
// second copy of this ladder is exactly how a live split row came to read
// as a ghost in the sky.
func RowKey(row Row) string {
	if row.ID != "" {
		return row.ID
	}
	if row.Socket != "" {
		return row.Kind.String() + "\x00" + row.Socket + "\x00" + row.PaneID
	}
	return row.Kind.String() + "\x00" + row.CWD + "\x00" + row.Project
}

// Output contains the rows plus project metadata. Composition reads the kill
// list; the store owns persistence and expiry of /clear prompt baselines.
type Output struct {
	Rows            []Row
	ProjectDirs     map[string]string
	ProjectOrder    []string
	KilledCount     int
	SuppressedCount int

	includeNewClaude   bool
	includeNewCodex    bool
	includeNewOpenCode bool
	primaryAccount     int
	primaryCodex       int
	primaryOpenCode    int
	fallbackDir        string
}
