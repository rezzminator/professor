package gather

// Pane is one live tmux pane from a chat socket.
//
// CurrentCommand is the pane's foreground command. It tells a chat pane apart
// from a VIEWPORT — a pane running `tmux attach` against another chat's socket,
// which mirrors that chat's statusline and would otherwise donate the inner
// chat's 🔖 label to the outer window's name.
type ProbePane struct {
	Socket         string
	SessionName    string
	WindowID       string
	WindowName     string
	PaneTitle      string
	CurrentPath    string
	CurrentCommand string
	TTY            string
	PID            int
	PaneID         string
	Attached       bool
}

// TmuxSnapshot is the live pane result plus recoverable sweep diagnostics.
type TmuxSnapshot struct {
	Panes         []ProbePane
	CorpseSwept   []string
	ProbeWarnings []string
}

// Crumb maps a socket or pane to its current Claude transcript.
type Crumb struct {
	Filename       string
	Socket         string
	PaneID         string
	TranscriptPath string
}

// CrumbProbe contains accepted crumbs and sweep counters.
type CrumbProbe struct {
	Crumbs     []Crumb
	FilesSeen  int
	Accepted   int
	Ignored    int
	StaleSwept []string
}

// LiveCodex maps a live codex process and its first rollout onto a pane.
// ThreadID is the conversation the process was resolved to; it is the only
// identity a session that writes no rollout file has.
type LiveCodex struct {
	PID         int
	PanePID     int
	Socket      string
	PaneID      string
	RolloutPath string
	ThreadID    string
	// RolloutHeld is true only when RolloutPath was found among the
	// process's own FDLinks in DetectCodexThreadsInRoots — the process
	// itself has the file open right now. It is false when RolloutPath (or
	// ThreadID) came from argv, an inherited environment variable, or the
	// state-store resolver: those name a conversation the process is NOT
	// currently holding open, and must never outrank what the pane's own
	// screen says.
	RolloutHeld bool
	// IdentityError refuses pane rebinding when a live process cannot prove its root.
	IdentityError string
}

// ClaudeProcess maps one live Claude process onto its owning tmux pane.
type ClaudeProcess struct {
	PID     int
	PanePID int
	Socket  string
	PaneID  string
	TTY     string
}

// Agent is one non-primary live Claude session identity.
type Agent struct {
	PID       int
	PanePID   int
	Socket    string
	PaneID    string
	SessionID string
	ConfigDir string
	StartTime uint64
}

// WindowRename is an unapplied Codex window convergence action.
type WindowRename struct {
	Socket      string
	SessionName string
	WindowID    string
	CurrentName string
	TargetName  string
}

// CrumblessLive is a live Claude pane whose socket carries no crumb yet: a
// chat still sitting at a startup prompt (folder trust, MCP approval) has a
// process the tree walk already finds, but the statusline writes the SID
// crumb only once boot completes. Crumb-driven gather is otherwise blind to
// it, so compose needs these fields to synthesize a placeholder row until the
// crumb lands. PaneStartUnix is the pane's own process birth (0 when the
// ProcFS in use cannot report one), which stands in for the socket-epoch
// activity fallback compose gives an ordinary live row — a cc-new-* socket
// carries no epoch of its own to fall back to.
type CrumblessLive struct {
	Socket        string
	SessionName   string
	WindowID      string
	WindowName    string
	PaneID        string
	PID           int
	CWD           string
	PaneStartUnix int64
}

// Snapshot is one immutable-by-convention gather result.
type Snapshot struct {
	Panes           []ProbePane
	Crumbs          []Crumb
	Codex           []LiveCodex
	ClaudeProcesses []ClaudeProcess
	Agents          []Agent
	Cache1HSockets  []string
	Renames         []WindowRename
	CrumblessLive   []CrumblessLive
	CorpseSwept     []string
	StaleSwept      []string
	Warnings        []string
}
