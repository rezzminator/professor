package reap

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

// ProcessTree is one snapshot of the machine's process relationships. Every
// socket's RAM sum and every foreign-process check reads from this one
// snapshot, so a sweep sees ONE consistent moment rather than a tree that
// shifts under it.
type ProcessTree struct {
	children map[int][]int
	cmdline  map[int][]string
	rssKB    map[int]int64
	matchers map[pfmengine.ID]gather.Matcher
	binaries map[pfmengine.ID]string
}

// NewProcessTree reads every process once.
func NewProcessTree(proc gather.ProcFS, binaries ...string) (*ProcessTree, error) {
	pids, err := proc.PIDs()
	if err != nil {
		return nil, fmt.Errorf("list processes for the reap sweep: %w", err)
	}
	memory, _ := proc.(gather.ProcMemory)
	tree := &ProcessTree{
		children: make(map[int][]int, len(pids)),
		cmdline:  make(map[int][]string, len(pids)),
		rssKB:    make(map[int]int64, len(pids)),
		matchers: make(map[pfmengine.ID]gather.Matcher, len(pfmengine.All())),
		binaries: make(map[pfmengine.ID]string, len(pfmengine.All())),
	}
	for index, id := range pfmengine.All() {
		if index >= len(binaries) {
			break
		}
		tree.binaries[id] = binaries[index]
	}
	for _, id := range pfmengine.All() {
		matcher, err := gather.MatcherFor(id)
		if err != nil {
			return nil, err
		}
		tree.matchers[id] = matcher
	}
	for _, pid := range pids {
		stat, err := proc.Stat(pid)
		if err != nil {
			// The process exited between the listing and the read. That is
			// ordinary, and it removes nothing a verdict depends on: a
			// process that is gone hosts nothing and holds no memory.
			continue
		}
		tree.children[stat.ParentPID] = append(tree.children[stat.ParentPID], pid)
		if cmdline, err := proc.Cmdline(pid); err == nil {
			tree.cmdline[pid] = cmdline
		}
		if memory != nil {
			if kilobytes, err := memory.RSSKB(pid); err == nil {
				tree.rssKB[pid] = kilobytes
			}
		}
	}
	for parent := range tree.children {
		sort.Ints(tree.children[parent])
	}
	return tree, nil
}

// SubtreeRSSKB sums the resident memory of a process and everything under it.
//
// It is an UPPER BOUND, not a measurement: RSS counts shared runtime pages
// once per process, so several node-based chats over-report by roughly half
// again. The honest reclaim figure is the machine's own available-memory
// delta across the reap, which the report prints beside this.
func (tree *ProcessTree) SubtreeRSSKB(roots ...int) int64 {
	var total int64
	tree.walk(roots, func(pid int) bool {
		total += tree.rssKB[pid]
		return true
	})
	return total
}

// ForeignProcesses names the processes these panes host that are not a chat.
//
// A chat pane is a SHELL, and a shell hosts whatever was typed into it: one
// socket on this very machine carries a project's pnpm, uv and node dev
// servers beside its chats, and `kill-server` would take them all down at
// once. So every pane is judged, leader included:
//
//   - a chat leader (claude, codex) exempts its WHOLE subtree — the MCP
//     servers and tool shells a chat spawns are the chat, and flagging them
//     would make every working chat unreapable;
//   - a shell leader is transparent, and the walk descends through it;
//   - anything else is foreign, and one is enough to make the socket
//     load-bearing however long it has been quiet.
func (tree *ProcessTree) ForeignProcesses(panePIDs []int) []string {
	found := make(map[string]struct{})
	for _, panePID := range panePIDs {
		tree.walk([]int{panePID}, func(pid int) bool {
			cmdline := tree.cmdline[pid]
			if tree.isChatProcess(cmdline) {
				return false
			}
			if isShellProcess(cmdline) {
				return true
			}
			if name := processName(cmdline); name != "" {
				found[name] = struct{}{}
			}
			return false
		})
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// walk visits each pid reachable from roots. visit reports whether to descend
// into that process's own children.
func (tree *ProcessTree) walk(roots []int, visit func(pid int) bool) {
	seen := make(map[int]struct{}, len(roots))
	stack := append([]int(nil), roots...)
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, repeated := seen[pid]; repeated {
			continue
		}
		seen[pid] = struct{}{}
		if !visit(pid) {
			continue
		}
		stack = append(stack, tree.children[pid]...)
	}
}

func (tree *ProcessTree) isChatProcess(cmdline []string) bool {
	for id, matcher := range tree.matchers {
		if matcher.IsCommand(cmdline, tree.binaries[id]) {
			return true
		}
	}
	return false
}

// shells is every process a chat pane legitimately sits in. A login shell
// arrives with a leading dash.
var shells = map[string]struct{}{
	"sh": {}, "bash": {}, "zsh": {}, "dash": {}, "ksh": {}, "fish": {},
	"login": {}, "tmux": {}, "su": {},
}

func isShellProcess(cmdline []string) bool {
	name := processName(cmdline)
	_, found := shells[strings.TrimPrefix(name, "-")]
	return found
}

func processName(cmdline []string) string {
	if len(cmdline) == 0 || cmdline[0] == "" {
		return ""
	}
	return filepath.Base(filepath.ToSlash(cmdline[0]))
}
