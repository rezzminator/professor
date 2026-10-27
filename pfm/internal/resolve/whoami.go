package resolve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

const (
	// ClaudeSessionEnv is exported into every shell a Claude Code chat spawns.
	ClaudeSessionEnv = "CLAUDE_CODE_SESSION_ID"

	// AncestryDepth bounds the walk up the process chain. Twelve is past normal
	// nesting and stops a pid-reuse cycle from spinning.
	AncestryDepth = 12
)

// Identity failures distinguish absence, a proven unregistered pane and conflicting owners.
var (
	ErrNotInChat = errors.New(
		"ERROR: this process is not inside a pfm chat: no live tmux pane or Herdr pane holds it",
	)
	ErrNotRegistered = errors.New("resolve: caller not registered")
	ErrAmbiguous     = errors.New("resolve: caller ambiguous")
)

// Identity is who the CALLING process is: the tmux handle another chat calls
// to, plus whatever engine identity the environment carries.
type Identity struct {
	Backend        string `json:"backend,omitempty"`
	SeatID         string `json:"seat_id,omitempty"`
	HerdrWorkspace string `json:"herdr_workspace,omitempty"`
	Fronted        bool   `json:"fronted,omitempty"`
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
	HerdrSocket     string
	HerdrPane       string
	TMUX            string
	TMUXPane        string
	ClaudeSessionID string
	CodexThreadID   string
}

// SessionNamer answers "what is the tmux session name here" for one socket.
type SessionNamer interface {
	SessionPane(ctx context.Context, socketPath, target string) (session string, panePID int, err error)
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
	CodexSeats  func(context.Context) ([]CodexSeat, error)
}

// Whoami resolves the calling chat's own identity.
type Whoami struct {
	environment WhoamiEnvironment
	namer       SessionNamer
	owners      PaneOwnerLister
	processes   ProcessTree
	selfPID     int
	tmuxDir     string
	codexSeats  func(context.Context) ([]CodexSeat, error)
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
		codexSeats:  dependencies.CodexSeats,
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
	return CallerEnvironmentFrom(paths.OSEnv{})
}

// CallerEnvironmentFrom snapshots identity variables over an injected environment.
func CallerEnvironmentFrom(env paths.Env) WhoamiEnvironment {
	return WhoamiEnvironment{
		TMUX:            env.Get("TMUX"),
		HerdrSocket:     env.Get("HERDR_SOCKET_PATH"),
		HerdrPane:       env.Get("HERDR_PANE_ID"),
		TMUXPane:        env.Get("TMUX_PANE"),
		ClaudeSessionID: env.Get(ClaudeSessionEnv),
		CodexThreadID:   env.Get(CodexThreadEnv),
	}
}

// Identify reads engine/transcript identity first, then proves the environment's
// tmux and Herdr claims against live servers and process ancestry. The tmux
// display supplies session and pane pid; Herdr supplies canonical pane identity
// and foreground pids. The nearest ancestor wins; equal claims are ambiguous.
// A tmux winner needs no row. A Herdr winner resolves its durable seat and
// supervisor only after location proof. Without a proven claim, ancestry
// recovery precedes the Codex-thread rung. Stale claims become not-in-chat only
// after those rungs fail; failed reads never become absence.
func (identifier *Whoami) Identify(ctx context.Context) (Identity, error) {
	identity := Identity{Source: tmuxName}
	if identifier.environment.ClaudeSessionID != "" {
		identity.Engine, identity.ID, identity.Source = string(
			pfmengine.Claude,
		), identifier.environment.ClaudeSessionID, "env-claude"
	} else if identifier.environment.CodexThreadID != "" {
		identity.Engine, identity.ID, identity.Source = string(
			pfmengine.Codex,
		), identifier.environment.CodexThreadID, "env-codex"
	}
	chain, ancestryErr := identifier.ancestors()
	tmuxIdentity, tmuxIndex, tmuxStale, err := identifier.proveTmux(
		ctx,
		identity,
		pfmtmux.SocketPath(identifier.environment.TMUX),
		identifier.environment.TMUXPane,
		chain,
	)
	if err != nil {
		return identity, err
	}
	native, herdrIndex, herdrStale, err := identifier.proveHerdr(ctx, chain)
	if err != nil {
		return identity, err
	}
	if tmuxIndex >= 0 && herdrIndex >= 0 && tmuxIndex == herdrIndex {
		return identity, fmt.Errorf("%w: tmux and Herdr own ancestor %d", ErrAmbiguous, chain[tmuxIndex])
	}
	if herdrIndex >= 0 && (tmuxIndex < 0 || herdrIndex < tmuxIndex) {
		native.Engine, native.ID = identity.Engine, identity.ID
		return identifier.identifyHerdr(ctx, native)
	}
	if tmuxIndex >= 0 {
		return tmuxIdentity, nil
	}
	if ancestryErr != nil {
		return identity, fmt.Errorf("look up the caller: process ancestry: %w", ancestryErr)
	}
	recovered, found, recoveryErr := identifier.recoverFromAncestry(ctx, identity, chain)
	if found {
		return recovered, nil
	}
	if identifier.codexSeats != nil && identifier.environment.CodexThreadID != "" &&
		identifier.environment.ClaudeSessionID == "" {
		seats, err := identifier.codexSeats(ctx)
		if err != nil {
			return identity, fmt.Errorf("look up the caller: list Codex seats: %w", err)
		}
		codex, err := CodexThreadSeat(identifier.environment.CodexThreadID, seats)
		if errors.Is(err, ErrNotInChat) && recoveryErr != nil {
			return identity, recoveryErr
		}
		return codex, err
	}
	if recoveryErr != nil {
		return identity, recoveryErr
	}
	stale := make([]string, 0, 2)
	if tmuxStale != "" {
		stale = append(stale, tmuxStale)
	}
	if herdrStale != "" {
		stale = append(stale, herdrStale)
	}
	if len(stale) > 0 {
		return identity, fmt.Errorf("%w (stale: %s)", ErrNotInChat, strings.Join(stale, "; "))
	}
	return identity, ErrNotInChat
}

func (identifier *Whoami) proveTmux(
	ctx context.Context,
	identity Identity,
	socket, pane string,
	chain []int,
) (Identity, int, string, error) {
	if socket == "" {
		return identity, -1, "", nil
	}
	session, pid, err := identifier.namer.SessionPane(ctx, socket, pane)
	if err != nil {
		var exit *exec.ExitError
		if gather.TmuxServerGone(err) ||
			errors.As(err, &exit) && strings.Contains(string(exit.Stderr), "can't find pane") {
			return identity, -1, fmt.Sprintf("tmux %s %s: %v", socket, pane, err), nil
		}
		return identity, -1, "", fmt.Errorf("look up the caller: tmux pane %s on %s: %w", pane, socket, err)
	}
	if session == "" || pid <= 0 {
		return identity, -1, "", fmt.Errorf(
			"look up the caller: tmux pane %s on %s returned malformed identity",
			pane,
			socket,
		)
	}
	for index, ancestor := range chain {
		if pid != ancestor {
			continue
		}
		identity.Backend, identity.Session = "tmux", session
		identity.SocketPath, identity.SocketName, identity.Pane = socket, filepath.Base(socket), pane
		if identity.Engine == "" {
			identity.Engine = engineForSocket(identity.SocketName)
		}
		return identity, index, "", nil
	}
	return identity, -1, fmt.Sprintf("tmux %s %s: pane pid %d is not an ancestor", socket, pane, pid), nil
}

// recoverFromAncestry proves inherited claims before trying live pane owners.
func (identifier *Whoami) recoverFromAncestry(
	ctx context.Context,
	identity Identity,
	chain []int,
) (Identity, bool, error) {
	var readErr error
	for _, pid := range chain {
		environment, err := identifier.processes.Environ(pid)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ESRCH) && readErr == nil {
				readErr = fmt.Errorf("look up the caller: read environment of pid %d: %w", pid, err)
			}
			continue
		}
		found, index, _, err := identifier.proveTmux(
			ctx,
			identity,
			pfmtmux.SocketPath(environment["TMUX"]),
			environment["TMUX_PANE"],
			chain,
		)
		if err != nil {
			return identity, false, err
		}
		if index >= 0 {
			found.Recovered = true
			if found.Source == tmuxName {
				found.Source = "ancestry"
			}
			return found, true, nil
		}
	}
	owners, err := identifier.paneOwners(ctx)
	if err != nil {
		return identity, false, err
	}
	for _, pid := range chain {
		for _, owner := range owners {
			if owner.PanePID != pid {
				continue
			}
			found, index, _, err := identifier.proveTmux(ctx, identity, owner.SocketPath, owner.PaneID, chain)
			if err != nil {
				return identity, false, err
			}
			if index >= 0 {
				found.Recovered = true
				if found.Source == tmuxName {
					found.Source = "ancestry"
				}
				return found, true, nil
			}
		}
	}
	return identity, false, readErr
}

func (identifier *Whoami) ancestors() ([]int, error) {
	chain := make([]int, 0, AncestryDepth)
	pid := identifier.selfPID
	for depth := 0; depth < AncestryDepth && pid > 1; depth++ {
		chain = append(chain, pid)
		parent, err := identifier.processes.Parent(pid)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
				break
			}
			return chain, fmt.Errorf("read parent of pid %d: %w", pid, err)
		}
		if parent <= 1 || parent == pid {
			break
		}
		pid = parent
	}
	return chain, nil
}

func (identifier *Whoami) paneOwners(ctx context.Context) ([]PaneOwner, error) {
	entries, err := os.ReadDir(identifier.tmuxDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look up the caller: list tmux sockets: %w", err)
	}
	owners := make([]PaneOwner, 0)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("look up the caller: stat tmux socket %s: %w", entry.Name(), err)
		}
		if info.Mode()&os.ModeSocket == 0 {
			continue
		}
		socketPath := filepath.Join(identifier.tmuxDir, entry.Name())
		rows, err := identifier.owners.PaneOwners(ctx, socketPath)
		if gather.TmuxServerGone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("look up the caller: read tmux panes on %s: %w", socketPath, err)
		}
		owners = append(owners, rows...)
	}
	return owners, nil
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

// SessionPane reads the target's session and pane pid in one display request.
func (namer CommandTmuxNamer) SessionPane(ctx context.Context, socketPath, target string) (string, int, error) {
	arguments := []string{"display-message", "-p"}
	if target != "" {
		arguments = append(arguments, "-t", target)
	}
	arguments = append(arguments, "#{session_name} #{pane_pid}")
	output, err := pfmtmux.Socket{Binary: namer.Binary}.Command(ctx, socketPath, arguments...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return "", 0, fmt.Errorf(
				"read tmux session and pane pid (%s): %w",
				strings.TrimSpace(string(exit.Stderr)),
				err,
			)
		}
		return "", 0, fmt.Errorf("read tmux session and pane pid: %w", err)
	}
	value := strings.TrimSpace(string(output))
	separator := strings.LastIndexByte(value, ' ')
	if separator < 1 {
		return "", 0, fmt.Errorf("tmux returned malformed session and pane pid %q", value)
	}
	session, rawPID := value[:separator], value[separator+1:]
	pid, parseErr := strconv.Atoi(rawPID)
	if parseErr != nil || pid <= 0 {
		return "", 0, fmt.Errorf("tmux returned malformed session and pane pid %q", value)
	}
	return session, pid, nil
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
	// Dir stays empty: socketPath is already the full pathname (lister.TmuxDir
	// addresses a directory to SCAN for sockets, in paneOwners above — never a
	// join prefix here), and filepath.Join("", full) returns full unchanged.
	output, err := pfmtmux.Socket{Binary: lister.Binary}.
		Command(ctx, socketPath, "list-panes", "-a", "-F", "#{pane_pid} #{pane_id}").
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
