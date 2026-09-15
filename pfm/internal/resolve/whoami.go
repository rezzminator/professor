package resolve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
	pfmtmux "hostops/pfm/internal/tmux"
)

const (
	// ClaudeSessionEnv is exported into every shell a Claude Code chat spawns.
	ClaudeSessionEnv = "CLAUDE_CODE_SESSION_ID"

	// AncestryDepth bounds the walk up the process chain. Twelve is past normal
	// nesting and stops a pid-reuse cycle from spinning.
	AncestryDepth = 12
)

// ErrNoTmux is the whoami failure chat.sh reports on stderr (chat.sh:94).
var ErrNoTmux = errors.New(
	"ERROR: this chat is not inside tmux ($TMUX unset, and no ancestor carries it)",
)

// Identity is who the CALLING process is: the tmux handle another chat injects
// to, plus whatever engine identity the environment carries.
type Identity struct {
	// Session is chat.sh whoami's stdout — this chat's own tmux session name.
	Session    string `json:"session"`
	SocketPath string `json:"socket_path,omitempty"`
	SocketName string `json:"socket_name,omitempty"`
	Pane       string `json:"pane,omitempty"`
	Engine     string `json:"engine,omitempty"`
	ID         string `json:"id,omitempty"`
	// Source records which rung answered: env, tmux, or ancestry.
	Source    string `json:"source"`
	Recovered bool   `json:"recovered,omitempty"`
}

// WhoamiEnvironment is the caller environment whoami reads. It is a value so a
// jail can state it outright instead of mutating the process environment.
type WhoamiEnvironment struct {
	TMUX            string
	TMUXPane        string
	ClaudeSessionID string
	CodexThreadID   string
}

// SessionNamer answers "what is the tmux session name here" for one socket.
type SessionNamer interface {
	SessionName(ctx context.Context, socketPath, target string) (string, error)
}

// PaneOwner is one live pane and the pid tmux runs in it.
type PaneOwner struct {
	SocketPath string
	PaneID     string
	PanePID    int
}

// PaneOwnerLister asks a tmux server which pids own its panes. It is the
// portable half of cc_pane_of: it asks tmux who it owns rather than asking the
// kernel what a process was told.
type PaneOwnerLister interface {
	PaneOwners(ctx context.Context, socketPath string) ([]PaneOwner, error)
}

// ProcessTree exposes the two /proc facts ancestry recovery needs.
type ProcessTree interface {
	Environ(pid int) (map[string]string, error)
	Parent(pid int) (int, error)
}

// WhoamiDependencies are injectable so the jail never touches live state.
type WhoamiDependencies struct {
	Environment *WhoamiEnvironment
	Namer       SessionNamer
	Owners      PaneOwnerLister
	Processes   ProcessTree
	SelfPID     int
	TmuxDir     string
	ProcRoot    string
}

// Whoami resolves the calling chat's own identity.
type Whoami struct {
	environment WhoamiEnvironment
	namer       SessionNamer
	owners      PaneOwnerLister
	processes   ProcessTree
	selfPID     int
	tmuxDir     string
}

// NewWhoami builds a resolver over the standard jail-aware paths.
func NewWhoami(dependencies WhoamiDependencies) (*Whoami, error) {
	resolved, err := paths.Resolve()
	if err != nil {
		return nil, err
	}
	tmuxDir := dependencies.TmuxDir
	if tmuxDir == "" {
		tmuxDir = resolved.TmuxDir
	}
	procRoot := dependencies.ProcRoot
	if procRoot == "" {
		procRoot = resolved.ProcRoot
	}
	var environment WhoamiEnvironment
	if dependencies.Environment != nil {
		environment = *dependencies.Environment
	} else {
		environment = CallerEnvironment()
	}
	identifier := &Whoami{
		environment: environment,
		namer:       dependencies.Namer,
		owners:      dependencies.Owners,
		processes:   dependencies.Processes,
		selfPID:     dependencies.SelfPID,
		tmuxDir:     tmuxDir,
	}
	if identifier.namer == nil {
		identifier.namer = CommandTmuxNamer{}
	}
	if identifier.owners == nil {
		identifier.owners = CommandPaneOwners{TmuxDir: tmuxDir}
	}
	if identifier.processes == nil {
		identifier.processes = ProcTree{Root: procRoot, ProcFS: newNativeProcFS(procRoot)}
	}
	if identifier.selfPID == 0 {
		identifier.selfPID = os.Getpid()
	}
	return identifier, nil
}

// CallerEnvironment snapshots the identity variables of this process.
func CallerEnvironment() WhoamiEnvironment {
	return WhoamiEnvironment{
		TMUX:            os.Getenv("TMUX"),
		TMUXPane:        os.Getenv("TMUX_PANE"),
		ClaudeSessionID: os.Getenv(ClaudeSessionEnv),
		CodexThreadID:   os.Getenv(CodexThreadEnv),
	}
}

// Identify answers chat.sh's `whoami`: this chat's own tmux session name, plus
// the engine identity behind it. The exported session id is authoritative for
// WHICH conversation this is; the tmux handle comes from $TMUX, or — when the
// engine spawned our shell without passing tmux context through, which is the
// codex path — from ANCESTRY RECOVERY over the process chain
// (chat.sh:65-96). A recovered value is never exported globally: an empty
// $TMUX is sometimes deliberate, and re-exporting it would point every probe
// at the sender's own session.
func (identifier *Whoami) Identify(ctx context.Context) (Identity, error) {
	identity := Identity{Source: "tmux"}
	if identifier.environment.ClaudeSessionID != "" {
		identity.Engine = string(pfmengine.Claude)
		identity.ID = identifier.environment.ClaudeSessionID
		identity.Source = "env-claude"
	} else if identifier.environment.CodexThreadID != "" {
		thread, err := CodexThreadID(
			identifier.environment.CodexThreadID,
			"",
			"",
			0,
			nil,
		)
		if err == nil {
			identity.ID = thread.ID
		}
		identity.Engine = string(pfmengine.Codex)
		identity.Source = "env-codex"
	}

	socketPath := socketFromTMUX(identifier.environment.TMUX)
	pane := ""
	if pane == "" {
		pane = identifier.environment.TMUXPane
	}
	if socketPath == "" {
		recoveredSocket, recoveredPane, ok := identifier.recoverFromAncestry(ctx)
		if !ok {
			return identity, ErrNoTmux
		}
		socketPath = recoveredSocket
		pane = recoveredPane
		identity.Recovered = true
		if identity.Source == "tmux" {
			identity.Source = "ancestry"
		}
	}
	identity.SocketPath = socketPath
	identity.SocketName = filepath.Base(socketPath)
	identity.Pane = pane
	if identity.Engine == "" {
		identity.Engine = engineForSocket(identity.SocketName)
	}
	session, err := identifier.namer.SessionName(ctx, socketPath, pane)
	if err != nil || session == "" {
		return identity, ErrNoTmux
	}
	identity.Session = session
	return identity, nil
}

// recoverFromAncestry walks this process's parents for a tmux context: first
// their environments (the exact answer when the kernel will give it), then the
// tmux servers' own pane-pid tables, which stay truthful even when a pane's
// command re-execs.
func (identifier *Whoami) recoverFromAncestry(
	ctx context.Context,
) (string, string, bool) {
	chain := identifier.ancestors()
	for _, pid := range chain {
		environment, err := identifier.processes.Environ(pid)
		if err != nil {
			continue
		}
		socketPath := socketFromTMUX(environment["TMUX"])
		pane := ""
		if socketPath == "" {
			continue
		}
		if pane == "" {
			pane = environment["TMUX_PANE"]
		}
		return socketPath, pane, true
	}
	owners := identifier.paneOwners(ctx)
	for _, pid := range chain {
		for _, owner := range owners {
			if owner.PanePID == pid {
				return owner.SocketPath, owner.PaneID, true
			}
		}
	}
	return "", "", false
}

func (identifier *Whoami) ancestors() []int {
	chain := make([]int, 0, AncestryDepth)
	pid := identifier.selfPID
	for depth := 0; depth < AncestryDepth && pid > 1; depth++ {
		chain = append(chain, pid)
		parent, err := identifier.processes.Parent(pid)
		if err != nil || parent <= 1 || parent == pid {
			break
		}
		pid = parent
	}
	return chain
}

func (identifier *Whoami) paneOwners(ctx context.Context) []PaneOwner {
	entries, err := os.ReadDir(identifier.tmuxDir)
	if err != nil {
		return nil
	}
	owners := make([]PaneOwner, 0)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			continue
		}
		socketPath := filepath.Join(identifier.tmuxDir, entry.Name())
		rows, err := identifier.owners.PaneOwners(ctx, socketPath)
		if err != nil {
			continue
		}
		owners = append(owners, rows...)
	}
	return owners
}

// socketFromTMUX splits a $TMUX value into its socket path. Only the socket
// component is ever read back; the session and pane fields of that string name
// a server-local index, not a stable identity.
func socketFromTMUX(value string) string {
	if value == "" {
		return ""
	}
	fields := strings.Split(value, ",")
	socketPath := fields[0]
	if socketPath == "" {
		return ""
	}
	return socketPath
}

func engineForSocket(socketName string) string {
	id, ok := pfmengine.FromSocket(socketName)
	if !ok {
		return "unknown"
	}
	return string(id)
}

// CommandTmuxNamer asks a tmux server for a session name over its socket path.
type CommandTmuxNamer struct {
	Binary string
}

// SessionName targets an explicit pane when one is known, so a socket hosting
// several sessions still answers with the caller's own.
func (namer CommandTmuxNamer) SessionName(
	ctx context.Context,
	socketPath, target string,
) (string, error) {
	arguments := []string{"display-message", "-p"}
	if target != "" {
		arguments = append(arguments, "-t", target)
	}
	arguments = append(arguments, "#{session_name}")
	output, err := pfmtmux.Command(ctx, namer.Binary, socketPath, arguments...).Output()
	if err != nil && target != "" {
		// A stale pane id must not kill a live session: retry untargeted, the
		// way chat.sh's bare `tmux display-message -p` does.
		return namer.SessionName(ctx, socketPath, "")
	}
	return strings.TrimSpace(string(output)), err
}

// CommandPaneOwners lists pane pids on one tmux socket.
type CommandPaneOwners struct {
	Binary  string
	TmuxDir string
}

// PaneOwners returns every pane on the socket with the pid running in it.
//
// Space-delimited, never a tab. tmux hands a control character in a format
// string back as "_" unless the caller's environment carries a UTF-8 locale or
// merely DEFINES $TMUX — the empty one pfmtmux.Command sets happens to satisfy that, which
// is the whole reason a tab worked here while the same tab in a scrubbed tool
// shell left ancestry recovery answering "not in tmux". A pane pid is digits and
// a pane id is %N, so a space cannot be ambiguous and nothing rests on the
// accident.
func (lister CommandPaneOwners) PaneOwners(
	ctx context.Context,
	socketPath string,
) ([]PaneOwner, error) {
	output, err := pfmtmux.Command(ctx, lister.Binary, socketPath, "list-panes", "-a", "-F", "#{pane_pid} #{pane_id}").
		Output()
	if err != nil {
		return nil, err
	}
	owners := make([]PaneOwner, 0)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		owners = append(owners, PaneOwner{
			SocketPath: socketPath,
			PaneID:     fields[1],
			PanePID:    pid,
		})
	}
	return owners, nil
}

// ProcFS is the small process-table seam ancestry recovery needs. Platform
// implementations provide Linux procfs or Darwin sysctl without making the
// resolver know which kernel it is running on.
type ProcFS interface {
	Environ(int) (map[string]string, error)
	Stat(int) (ProcStat, error)
}

// ProcStat contains the process relationship needed by ancestry recovery.
type ProcStat struct {
	ParentPID int
}

// ProcTree reads through ProcFS. Root defaults to the platform's native
// process table and is overridden by a jail on Linux.
type ProcTree struct {
	Root   string
	ProcFS ProcFS
}

// Environ returns one process's environment, keyed before the first equals.
func (tree ProcTree) Environ(pid int) (map[string]string, error) {
	return tree.procfs().Environ(pid)
}

// Parent returns the parent pid through the platform process abstraction.
func (tree ProcTree) Parent(pid int) (int, error) {
	stat, err := tree.procfs().Stat(pid)
	if err != nil {
		return 0, err
	}
	return stat.ParentPID, nil
}

func (tree ProcTree) procfs() ProcFS {
	if tree.ProcFS != nil {
		return tree.ProcFS
	}
	return newNativeProcFS(tree.Root)
}
