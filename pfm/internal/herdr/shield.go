package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

// herdr-shield is the Herdr plugin whose holder runs a pane's shell in a systemd
// user service: Herdr's default_shell is its attach client, so Herdr's
// pane.process_info names that client while the real shell lives outside
// Herdr's process tree. The shell's HERDR_SOCKET_PATH is the holder's private
// relay (herdr-shield spec § Holder protocol names it herdr-api.sock) and its
// HERDR_PANE_ID the pane it was born in. `herdr-shield list --json` is its
// public contract; pfm reads nothing else of it.
const (
	ShieldBinary = "herdr-shield"
	// EnvShieldInner marks a held shell's environment; herdr-shield refuses to run
	// under it (spec § herdr: recursive keeper invocation).
	EnvShieldInner    = "HERDR_SHIELD_INNER"
	shieldRelaySocket = "herdr-api.sock"
	shieldListBound   = 10 * time.Second
	shieldListFresh   = 2 * time.Second
	shieldRunning     = "running"
	shieldProtected   = "PROTECTED"
	shieldListed      = "OK"
)

// Process is one foreground process of a pane.
type Process struct {
	PID   int      `json:"pid"`
	Name  string   `json:"name"`
	Argv0 string   `json:"argv0"`
	Argv  []string `json:"argv"`
}

// PaneProcesses is a pane's processes as the shell inside it sees them: Herdr's
// pane.process_info, or for a pane herdr-shield holds, the holder's child as the
// shell and the holder's terminal foreground group as the foreground.
type PaneProcesses struct {
	// Pane is the canonical pane id Herdr answered for.
	Pane string
	// Shell is the pane's shell pid; 0 when Herdr names none.
	Shell int
	// Foreground is nil when Herdr's foreground_processes was missing, null or
	// unreadable; ForegroundErr names the unreadable one. A shell read needs neither.
	Foreground    []Process
	ForegroundErr error
	// Holder is the herdr-shield holder running the pane's shell; nil when Herdr runs it.
	Holder *ShieldHolder
}

// PIDs are the foreground pids; a foreground Herdr did not list is an error,
// never a pane running nothing.
func (processes PaneProcesses) PIDs() ([]int, error) {
	if processes.ForegroundErr != nil {
		return nil, processes.ForegroundErr
	}
	if processes.Foreground == nil {
		return nil, fmt.Errorf(
			"herdr pane.process_info of %q: foreground_processes missing or null", processes.Pane,
		)
	}
	pids := make([]int, 0, len(processes.Foreground))
	for _, process := range processes.Foreground {
		pids = append(pids, process.PID)
	}
	return pids, nil
}

// ShieldOuterEnviron is environ with a held shell's marks stripped (EnvShieldInner,
// and the pfm Herdr shell mark envPfmShell), so herdr-shield run from inside a
// held pane checks the keeper instead of refusing a recursive invocation.
func ShieldOuterEnviron(environ []string) []string {
	return without(environ, []string{EnvShieldInner, envPfmShell})
}

// envPfmShell marks a shell the pfm Herdr shell shim started (pfm-herdr-shell.sh).
const envPfmShell = "PFM_HERDR_SHELL"

// ShieldProcess is a process herdr-shield identifies by pid and /proc start time.
type ShieldProcess struct {
	PID   int    `json:"pid"`
	Start uint64 `json:"start"`
}

// ShieldHolder is one holder of `herdr-shield list --json`.
type ShieldHolder struct {
	ID      string        `json:"id"`
	Live    bool          `json:"holder_live"`
	State   string        `json:"state"`
	Orphan  bool          `json:"orphan"`
	Holder  ShieldProcess `json:"holder"`
	Child   ShieldProcess `json:"child"`
	Binding struct {
		Pane string `json:"pane"`
		// Socket is the upstream Herdr socket, never the holder's relay.
		Socket string `json:"socket"`
	} `json:"binding"`
}

func (holder ShieldHolder) running() bool {
	return holder.Live && holder.State == shieldRunning && !holder.Orphan
}

// ProcStat is the /proc/{pid}/stat fields the shield door reads.
type ProcStat struct {
	Start         uint64
	Group         int
	TerminalGroup int
}

// Proc is the process table the shield door verifies holders against
// (gather.HerdrShield adapts gather's ProcFS).
type Proc interface {
	PIDs() ([]int, error)
	Cmdline(pid int) ([]string, error)
	Stat(pid int) (ProcStat, error)
}

// Shield reads herdr-shield for the panes it holds. A listing of a Herdr socket
// serves every read of that socket within shieldListFresh of it, so one
// operation runs herdr-shield once per socket.
type Shield struct {
	Runner deps.Runner
	Proc   Proc

	mu       sync.Mutex
	listings map[string]shieldList
}

// shieldList is one `herdr-shield list --json` run against one Herdr socket.
type shieldList struct {
	at      time.Time
	holders []ShieldHolder
	panes   map[string]string
	err     error
}

// ParsePaneProcesses decodes a pane.process_info result as Herdr answered it.
func ParsePaneProcesses(body json.RawMessage) (PaneProcesses, error) {
	var decoded struct {
		Type string `json:"type"`
		Info struct {
			Pane       string          `json:"pane_id"`
			Shell      int             `json:"shell_pid"`
			Foreground json.RawMessage `json:"foreground_processes"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return PaneProcesses{}, fmt.Errorf("herdr pane.process_info: decode result %q: %w", body, err)
	}
	if decoded.Type != processInfoResultType {
		return PaneProcesses{}, fmt.Errorf(
			"herdr pane.process_info: result type %q, want %s", decoded.Type, processInfoResultType,
		)
	}
	processes := PaneProcesses{Pane: decoded.Info.Pane, Shell: decoded.Info.Shell}
	if raw := decoded.Info.Foreground; len(raw) > 0 {
		if err := json.Unmarshal(raw, &processes.Foreground); err != nil {
			processes.Foreground, processes.ForegroundErr = nil, fmt.Errorf(
				"herdr pane.process_info of %q: decode foreground_processes %s: %w", processes.Pane, raw, err,
			)
		}
	}
	return processes, nil
}

// Processes decodes a pane.process_info result asked of socket and, when the
// pane's foreground is herdr-shield's attach client, swaps in the shell its bound
// holder runs: the holder's child as Shell and the holder terminal's
// foreground group as Foreground, each verified by pid and start time. A pane
// whose attach client has no running holder (herdr-shield's recovery screen)
// keeps Herdr's view. A holder herdr-shield could not list is an error, never
// a pane without a chat.
func (shield *Shield) Processes(ctx context.Context, socket string, body json.RawMessage) (PaneProcesses, error) {
	processes, err := ParsePaneProcesses(body)
	if err != nil || !shieldAttached(processes.Foreground) {
		return processes, err
	}
	holder, err := shield.paneHolder(ctx, socket, processes.Pane)
	if err != nil || holder == nil {
		return processes, err
	}
	foreground, err := shield.innerForeground(*holder)
	if err != nil {
		return processes, fmt.Errorf("herdr pane %s: herdr-shield holder %s: %w", processes.Pane, holder.ID, err)
	}
	processes.Shell, processes.Foreground, processes.Holder = holder.Child.PID, foreground, holder
	return processes, nil
}

// Read asks socket for pane's pane.process_info and returns Processes of it.
func (shield *Shield) Read(ctx context.Context, socket, pane string) (PaneProcesses, error) {
	body, err := paneProcessInfo(ctx, socket, pane)
	if err != nil {
		return PaneProcesses{}, err
	}
	return shield.Processes(ctx, socket, body)
}

// paneProcessInfo is the one pane.process_info request of a pane on socket.
func paneProcessInfo(ctx context.Context, socket, pane string) (json.RawMessage, error) {
	return SeatRequest(ctx, socket, "pane.process_info", map[string]string{paneIDKey: pane})
}

// ShieldForeground reports whether herdr-shield's attach client is pane's foreground
// on socket, where Herdr sees no engine process for the agent it tracks.
func ShieldForeground(ctx context.Context, socket, pane string) (bool, error) {
	body, err := paneProcessInfo(ctx, socket, pane)
	if err != nil {
		return false, err
	}
	processes, err := ParsePaneProcesses(body)
	if err != nil {
		return false, err
	}
	return shieldAttached(processes.Foreground), processes.ForegroundErr
}

// Upstream maps the Herdr socket and pane a process's environment names to
// Herdr's own: a herdr-shield relay socket and the birth pane beside it become
// the holder's bound upstream socket and current pane. Any other socket comes
// back unchanged without running herdr-shield.
func (shield *Shield) Upstream(ctx context.Context, socket, pane string) (string, string, error) {
	if filepath.Base(socket) != shieldRelaySocket {
		return socket, pane, nil
	}
	holder, err := shield.relayHolder(ctx, socket)
	if err != nil {
		return socket, pane, err
	}
	return holder.Binding.Socket, holder.Binding.Pane, nil
}

func shieldAttached(foreground []Process) bool {
	for _, process := range foreground {
		names := []string{process.Name, filepath.Base(process.Argv0)}
		if len(process.Argv) > 0 {
			names = append(names, filepath.Base(process.Argv[0]))
		}
		for _, name := range names {
			if name == ShieldBinary {
				return true
			}
		}
	}
	return false
}

// relayHolder is the running holder whose relay socket is socket: the relay
// lives in a directory named after its holder.
func (shield *Shield) relayHolder(ctx context.Context, socket string) (ShieldHolder, error) {
	holders, _, err := shield.list(ctx, socket)
	if err != nil {
		return ShieldHolder{}, fmt.Errorf("map herdr-shield relay %s upstream: %w", socket, err)
	}
	id := filepath.Base(filepath.Dir(socket))
	for _, holder := range holders {
		if holder.ID != id {
			continue
		}
		if !holder.running() || holder.Binding.Socket == "" || holder.Binding.Pane == "" {
			return ShieldHolder{}, fmt.Errorf(
				"map herdr-shield relay %s upstream: holder %s is %s (live %t, orphan %t)",
				socket, id, holder.State, holder.Live, holder.Orphan,
			)
		}
		return holder, nil
	}
	return ShieldHolder{}, fmt.Errorf("map herdr-shield relay %s upstream: herdr-shield lists no holder %s", socket, id)
}

// paneHolder is the running holder bound to pane on socket, nil when
// herdr-shield does not protect the pane.
func (shield *Shield) paneHolder(ctx context.Context, socket, pane string) (*ShieldHolder, error) {
	upstream, _, err := shield.Upstream(ctx, socket, pane)
	if err != nil {
		return nil, err
	}
	holders, panes, err := shield.list(ctx, upstream)
	if err != nil {
		return nil, fmt.Errorf("herdr pane %s runs herdr-shield: %w", pane, err)
	}
	var bound *ShieldHolder
	for index := range holders {
		holder := &holders[index]
		if holder.Binding.Socket != upstream || holder.Binding.Pane != pane || !holder.running() {
			continue
		}
		if bound != nil {
			return nil, fmt.Errorf("herdr pane %s: herdr-shield binds holders %s and %s", pane, bound.ID, holder.ID)
		}
		bound = holder
	}
	if bound == nil && panes[pane] == shieldProtected {
		return nil, fmt.Errorf("herdr pane %s: herdr-shield marks it %s but lists no running holder bound to it on %s",
			pane, shieldProtected, upstream)
	}
	return bound, nil
}

// list runs `herdr-shield list --json` against the Herdr server at socket, or
// answers from a listing of it still fresh. herdr-shield enumerates the server
// HERDR_SOCKET_PATH names and refuses to run under a held shell's
// HERDR_SHIELD_INNER, which pfm inherits when it runs in one.
// A nil Shield reads no holder: every herdr-shield question to it is an error.
func (shield *Shield) list(ctx context.Context, socket string) ([]ShieldHolder, map[string]string, error) {
	if shield == nil {
		return nil, nil, errors.New("no herdr-shield reader is wired here")
	}
	shield.mu.Lock()
	defer shield.mu.Unlock()
	now := clock.Real.Now()
	if listed, found := shield.listings[socket]; found && now.Sub(listed.at) < shieldListFresh {
		return listed.holders, listed.panes, listed.err
	}
	runner := shield.Runner
	if runner == nil {
		runner = deps.RealRunner{}
	}
	bounded, cancel := context.WithTimeout(ctx, shieldListBound)
	defer cancel()
	listed := shieldList{at: now}
	shieldPath, resolveErr := deps.Resolve(ShieldBinary)
	var result deps.RunResult
	var err error
	if resolveErr == nil {
		result, err = runner.Run(bounded, []string{shieldPath, "list", "--json"}, deps.RunOptions{
			Env:          append(without(os.Environ(), []string{EnvShieldInner, EnvSocket}), EnvSocket+"="+socket),
			WaitDelay:    2 * time.Second,
			ProcessGroup: true,
		})
	}
	switch {
	case resolveErr != nil:
		listed.err = fmt.Errorf("resolve %s: %w", ShieldBinary, resolveErr)
	case err != nil:
		listed.err = fmt.Errorf("run %s list --json against %s: %w", ShieldBinary, socket, err)
	case result.ExitCode != 0:
		listed.err = fmt.Errorf(
			"%s list --json against %s: exit %d: %s",
			ShieldBinary,
			socket,
			result.ExitCode,
			deps.FirstLine(string(result.Stderr)),
		)
	default:
		listed.holders, listed.panes, listed.err = parseShieldList(result.Stdout)
	}
	// A run its caller cut short says nothing about herdr-shield: never serve it on.
	if ctx.Err() == nil {
		if shield.listings == nil {
			shield.listings = map[string]shieldList{}
		}
		shield.listings[socket] = listed
	}
	return listed.holders, listed.panes, listed.err
}

func parseShieldList(stdout []byte) ([]ShieldHolder, map[string]string, error) {
	var listing struct {
		Enumeration string         `json:"enumeration"`
		Error       *string        `json:"error"`
		Holders     []ShieldHolder `json:"holders"`
		Panes       []struct {
			ID     string `json:"pane_id"`
			Status string `json:"status"`
		} `json:"panes"`
	}
	if err := json.Unmarshal(stdout, &listing); err != nil {
		return nil, nil, fmt.Errorf("decode %s list --json: %w", ShieldBinary, err)
	}
	if listing.Enumeration != shieldListed {
		reason := "no error given"
		if listing.Error != nil {
			reason = *listing.Error
		}
		return nil, nil, fmt.Errorf("%s list --json: enumeration %q: %s", ShieldBinary, listing.Enumeration, reason)
	}
	panes := make(map[string]string, len(listing.Panes))
	for _, pane := range listing.Panes {
		panes[pane.ID] = pane.Status
	}
	return listing.Holders, panes, nil
}

// innerForeground verifies the holder and its child by start time, then
// returns the processes of the foreground group of the child's terminal: none
// once the group's last member exited, as Herdr reports such a plain pane.
func (keep *Shield) innerForeground(holder ShieldHolder) ([]Process, error) {
	if shield.Proc == nil {
		return nil, errors.New("no process table to verify the holder against")
	}
	var child ProcStat
	for _, owned := range []struct {
		role    string
		process ShieldProcess
	}{{"holder", holder.Holder}, {"child", holder.Child}} {
		stat, err := shield.Proc.Stat(owned.process.PID)
		if err != nil {
			return nil, fmt.Errorf("read %s %d: %w", owned.role, owned.process.PID, err)
		}
		if owned.process.PID <= 1 || stat.Start != owned.process.Start {
			return nil, fmt.Errorf("%s pid %d started at %d, herdr-shield recorded %d: not the process it lists",
				owned.role, owned.process.PID, stat.Start, owned.process.Start)
		}
		child = stat
	}
	if child.TerminalGroup <= 0 {
		return nil, fmt.Errorf("child %d has no terminal foreground group", holder.Child.PID)
	}
	pids, err := shield.Proc.PIDs()
	if err != nil {
		return nil, fmt.Errorf("enumerate processes: %w", err)
	}
	foreground := make([]Process, 0, 1)
	for _, pid := range pids {
		stat, err := shield.Proc.Stat(pid)
		if gone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read process %d: %w", pid, err)
		}
		if stat.Group != child.TerminalGroup {
			continue
		}
		argv, err := shield.Proc.Cmdline(pid)
		// An empty cmdline is a member that exited and awaits its reaper.
		if gone(err) || (err == nil && len(argv) == 0) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read foreground process %d: %w", pid, err)
		}
		foreground = append(foreground, Process{PID: pid, Name: filepath.Base(argv[0]), Argv0: argv[0], Argv: argv})
	}
	return foreground, nil
}

// ShellEnded reports whether holder's child, the held shell, has exited: its
// pid is gone or now names a process started later.
func (shield *Shield) ShellEnded(holder ShieldHolder) (bool, error) {
	if shield == nil || shield.Proc == nil {
		return false, errors.New("no process table to verify the held shell against")
	}
	stat, err := shield.Proc.Stat(holder.Child.PID)
	if gone(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read held shell %d: %w", holder.Child.PID, err)
	}
	return stat.Start != holder.Child.Start, nil
}

// gone is a process that exited between the enumeration and the read.
func gone(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH)
}
