package action

import (
	"context"
	"io"

	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
)

// Route is the legacy picker action letter.
type Route byte

const (
	NewClaude    Route = 'N'
	NewCodex     Route = 'C'
	NewOpenCode  Route = 'P'
	Live         Route = 'L'
	Agent        Route = 'A'
	ResumeClaude Route = 'R'
	ResumeCodex  Route = 'X'
	// ResumeOpenCode takes the next free letter; 'O' reads as OpenCode.
	ResumeOpenCode Route = 'O'
)

// Request is every value needed to synthesize and prepare one selected row.
type Request struct {
	Row compose.Row
	// Prompt is optional initial work for a fresh engine picker row. Resume and
	// live routes reject/ignore no hidden input: cmd/pfm uses it only for the
	// explicit Professor-update banner.
	Prompt         string
	PrimaryAccount int
	Cache1H        bool
	Bunker         bool
	Home           string
	FreshSocket    string
	CurrentTMUX    string
	Config         pfmconfig.Config
}

// ChatServer is a detached server that must exist before its attach line is
// emitted. The executor creates it through spawn.TmuxSpawner.NewSession, the
// one chat-server creator, so a picker-born chat carries the same server
// options as every other door's.
type ChatServer struct {
	Socket string
	CWD    string
	// Window is the name the chat's window is born with: the engine's short
	// name until name-sync converges it onto the chat's own name.
	Window string
	Run    string
	// Titles is the resolved tmux.titles policy Synthesize read off the
	// machine config. NIL is the default (pfm owns the terminal title), never
	// "off": a plan built without a config keeps today's behaviour instead of
	// silently handing the title to the host.
	Titles *pfmconfig.TmuxTitles
}

// Plan contains the pure run string and the one eval line. Line never carries
// a trailing newline; the CLI writer owns the sole terminating newline.
type Plan struct {
	Route      Route
	Run        string
	Line       string
	ChatServer *ChatServer
}

// Pane is the tmux state needed by solo and self-switch.
type ActionPane struct {
	PaneID         string
	TTY            string
	SessionName    string
	WindowName     string
	WindowIndex    int
	CurrentCommand string
}

// TmuxClient contains only action mutations and prerequisite probes.
type TmuxClient interface {
	ListPanes(ctx context.Context, socket string) ([]ActionPane, error)
	SocketAlive(ctx context.Context, socket string) bool
	KillPane(ctx context.Context, socket, paneID string) error
	KillServer(ctx context.Context, socket string) error
	SetWindowSizeLatest(ctx context.Context, socket string) error
	SelectWindow(ctx context.Context, socket string, windowIndex int) error
	CreateChatServer(ctx context.Context, server ChatServer) error
}

// Process is one candidate for _cc_solo's stray-Claude sweep.
type Process struct {
	PID  int
	Argv []string
	TTY  string
}

// ProcessTable supplies process snapshots and TERM without exposing /proc to
// the action core.
type ProcessTable interface {
	Processes(ctx context.Context) ([]Process, error)
	Terminate(pid int) error
}

// Gate asks on /dev/tty whether a mismatched live Claude chat should reboot.
type Gate interface {
	Confirm(ctx context.Context, request GateRequest) (bool, error)
}

// GateRequest is the live birth environment versus this picker invocation.
type GateRequest struct {
	Name           string
	BirthAccount   int
	PrimaryAccount int
	BirthCache1H   bool
	WantCache1H    bool
}

// CommandRunner invokes the unchanged satellite argv contracts.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

// Dependencies replace every effect boundary in jailed tests.
type Dependencies struct {
	Tmux      TmuxClient
	Processes ProcessTable
	Gate      Gate
	Runner    CommandRunner
	Stderr    io.Writer
	// Heal repairs a Codex thread's history projection before it is resumed,
	// reporting in one line what it repaired and saying nothing when there
	// was nothing to repair. It is injected rather than imported because the
	// repair reads Codex's own SQLite stores, and action must stay free of
	// the store layer. A nil Heal simply skips the check.
	Heal HealFunc
}

// HealFunc is the pre-resume Codex projection repair.
type HealFunc func(ctx context.Context, threadID string) string

// Executor performs preparations and returns the only line the caller may
// write to stdout.
type Executor struct {
	tmux      TmuxClient
	processes ProcessTable
	gate      Gate
	runner    CommandRunner
	stderr    io.Writer
	sidDir    string
	heal      HealFunc
}
