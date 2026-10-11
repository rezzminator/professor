package gather

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// PresenceProbe is the picker's parked-poll question — "did the set of chats
// change?" — answered WITHOUT a fork or exec. A full pass costs seconds of CPU
// (a tmux fork+exec per socket plus a per-process scan), which is why a parked
// picker stops running them; but a parked picker that never looks again shows a
// fleet frozen at the moment it parked, long after chats launched, exited or
// died. Everything here is a syscall on a name pfm already knows: a directory
// listing, a unix connect(), a per-pid start-time lookup, and a read of small
// crumb files — nothing that spawns a process.
//
// A chat appears as a new tmux socket (and a crumb once its statusline runs),
// and disappears as a socket that is gone or refusing and as pane and engine
// processes that are gone or replaced; those are the signals, in that order of
// cost. What the probe does NOT see is a change that moves none of them — a
// rename, a store-side hide — and those wait for the next full pass.
type PresenceProbe struct {
	// TmuxDir and SIDDir are the chat socket directory and the crumb directory.
	TmuxDir string
	SIDDir  string
	// Proc answers a pid's start time or fs.ErrNotExist.
	Proc ProcFS
	// Dial reports whether the socket at the path still accepts a connection;
	// nil is tmux.Accepts, a connect() with no fork or exec.
	Dial func(path string) error
}

// Files fingerprints what the filesystem says about the fleet: the chat socket
// files (name and creation stamp), every valid crumb by name, and each pane
// crumb's content too — never mtime, which the statusline rewrites every tick.
// A bare socket crumb is fingerprinted by name alone: every pane of a split
// socket rewrites it with its own transcript on each statusline tick, so its
// content flips with no chat born or gone, while each pane's own crumb carries
// that pane's transcript change. A directory that cannot be read is an error,
// never an empty fingerprint: "could not look" must not equal "nothing there".
func (probe PresenceProbe) Files() (string, error) {
	var out strings.Builder
	entries, err := os.ReadDir(probe.TmuxDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		out.WriteString("tmux:absent\n")
	case err != nil:
		return "", fmt.Errorf("presence: read tmux socket directory %q: %w", probe.TmuxDir, err)
	default:
		for _, entry := range entries {
			if !isChatSocketName(entry.Name()) {
				continue
			}
			info, err := entry.Info()
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return "", fmt.Errorf("presence: stat tmux socket %q: %w", entry.Name(), err)
			}
			if info.Mode()&os.ModeSocket == 0 {
				continue
			}
			fmt.Fprintf(&out, "socket:%s:%d\n", entry.Name(), info.ModTime().UnixNano())
		}
	}
	crumbs, err := os.ReadDir(probe.SIDDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		out.WriteString("sid:absent\n")
	case err != nil:
		return "", fmt.Errorf("presence: read sid directory %q: %w", probe.SIDDir, err)
	default:
		for _, entry := range crumbs {
			if !entry.Type().IsRegular() {
				continue
			}
			_, paneID, ok := ParseCrumbName(entry.Name())
			if !ok {
				continue
			}
			if paneID == "" {
				fmt.Fprintf(&out, "crumb:%s\n", entry.Name())
				continue
			}
			content, err := readPaneCrumb(filepath.Join(probe.SIDDir, entry.Name()))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return "", fmt.Errorf("presence: read crumb %q: %w", entry.Name(), err)
			}
			fmt.Fprintf(&out, "crumb:%s:%s\n", entry.Name(), strings.TrimRight(content, "\r\n"))
		}
	}
	return out.String(), nil
}

// maxCrumbBytes bounds one crumb read: a crumb holds one transcript path.
const maxCrumbBytes = 4096

// readPaneCrumb reads one crumb for the 2 s parked poll without trusting the
// directory listing it came from: the open neither follows a link nor blocks
// on a FIFO swapped in after the listing, the opened file must be regular, and
// at most maxCrumbBytes are read. A crumb that breaks any of these is an error.
func readPaneCrumb(path string) (_ string, err error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("open crumb: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close crumb: %w", closeErr)
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat crumb: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("crumb is not a regular file (mode %s)", info.Mode())
	}
	content, err := io.ReadAll(io.LimitReader(file, maxCrumbBytes+1))
	if err != nil {
		return "", fmt.Errorf("read crumb: %w", err)
	}
	if len(content) > maxCrumbBytes {
		return "", fmt.Errorf("crumb is larger than %d bytes", maxCrumbBytes)
	}
	return string(content), nil
}

// Procs fingerprints the live snapshot's own processes and sockets: every pid
// it names with its start time (a reused pid is a different process), and
// whether each socket hosting a live pane still accepts. settled is false when
// a pid the snapshot lists was already gone — the snapshot is older than the
// moment of the probe, so the caller owes it one fresh pass.
func (probe PresenceProbe) Procs(live Snapshot) (fingerprint string, settled bool, err error) {
	pids := livePIDs(live)
	var out strings.Builder
	settled = true
	for _, pid := range pids {
		stat, statErr := probe.Proc.Stat(pid)
		switch {
		case errors.Is(statErr, fs.ErrNotExist):
			settled = false
			fmt.Fprintf(&out, "pid:%d:gone\n", pid)
		case statErr != nil:
			return "", false, fmt.Errorf("presence: stat pid %d: %w", pid, statErr)
		default:
			fmt.Fprintf(&out, "pid:%d:%d\n", pid, stat.StartTime)
		}
	}
	dial := probe.Dial
	if dial == nil {
		dial = pfmtmux.Accepts
	}
	for _, socket := range socketsHostingPanes(live) {
		state := "up"
		if dialErr := dial(filepath.Join(probe.TmuxDir, socket)); dialErr != nil {
			state = "down"
		}
		fmt.Fprintf(&out, "dial:%s:%s\n", socket, state)
	}
	return out.String(), settled, nil
}

// livePIDs is every positive pid the snapshot names, sorted and unique.
func livePIDs(live Snapshot) []int {
	seen := make(map[int]struct{})
	add := func(pid int) {
		if pid > 0 {
			seen[pid] = struct{}{}
		}
	}
	for index := range live.Panes {
		add(live.Panes[index].PID)
	}
	for _, process := range live.ClaudeProcesses {
		add(process.PID)
		add(process.PanePID)
	}
	for _, codex := range live.Codex {
		add(codex.PID)
		add(codex.PanePID)
	}
	for _, openCode := range live.OpenCode {
		add(openCode.PID)
		add(openCode.PanePID)
	}
	for _, agent := range live.Agents {
		add(agent.PID)
		add(agent.PanePID)
	}
	for _, crumbless := range live.CrumblessLive {
		add(crumbless.PID)
	}
	pids := make([]int, 0, len(seen))
	for pid := range seen {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids
}

// socketsHostingPanes is every socket hosting a pane of the snapshot, sorted and unique.
func socketsHostingPanes(live Snapshot) []string {
	seen := make(map[string]struct{})
	for index := range live.Panes {
		if socket := live.Panes[index].Socket; socket != "" {
			seen[socket] = struct{}{}
		}
	}
	sockets := make([]string, 0, len(seen))
	for socket := range seen {
		sockets = append(sockets, socket)
	}
	sort.Strings(sockets)
	return sockets
}
