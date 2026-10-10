package statusline

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// agentNesting is what a row says about the agents below its task, at every
// depth: how many there are, how many are working right now (their own turn
// is open), and how many could not be read. err is a failure to look (the
// subagents directory or a meta file could not be read) and renders "?/?",
// never as "none".
type agentNesting struct {
	total   int
	active  int
	unknown int
	err     error
}

// agentTree is one scan of the session's subagents directory: every agent's
// parent from its meta file's parentAgentId, which Claude Code writes for an
// agent spawned by another agent (a main-loop spawn carries none). open caches
// each agent's own turn state for the render, spent each agent's billing.
// over and now (judgeLiveness) decide which agents with an open turn really
// work: a transcript cannot say an agent was killed.
type agentTree struct {
	dir      string
	children map[string][]string
	err      error
	warnings []string
	open     map[string]turnState
	spent    map[string]agentSpend
	ids      []string
	over     map[string]bool
	now      time.Time
}

type turnState int

const (
	turnUnread turnState = iota
	turnClosed
	turnWorking
)

// agentDeadAfter is how long an agent's open turn may stay silent and still
// count as working. The longest quiet a live agent has is one foreground tool
// call (Bash stops at ten minutes) or one long model reply; an agent killed
// mid-stream, or stopped by its parent, leaves a last line nothing ever
// answers, so past this its open turn is a dead one.
const agentDeadAfter = 15 * time.Minute

// tailWindow is the first read from a transcript's end; it doubles until a
// message entry is found, so one huge tool result costs a longer read, never
// a wrong answer.
const tailWindow = 64 << 10

// scanAgentTree reads every agent-<id>.meta.json beside the session's
// transcript once per render. A directory that cannot be listed, or any meta
// file that cannot be read, sets err: the parent of that agent is unknown, so
// no row may claim a count it cannot vouch for.
func scanAgentTree(sessionTranscript string) *agentTree {
	tree := &agentTree{children: map[string][]string{}, open: map[string]turnState{}, spent: map[string]agentSpend{}}
	if strings.TrimSpace(sessionTranscript) == "" {
		tree.err = errors.New("payload names no session transcript")
		return tree
	}
	tree.dir = transcript.SubagentsDir(sessionTranscript)
	entries, err := os.ReadDir(tree.dir)
	if err != nil {
		tree.err = fmt.Errorf("list %s: %w", tree.dir, err)
		tree.warnings = append(tree.warnings, tree.err.Error())
		return tree
	}
	for _, entry := range entries {
		name := entry.Name()
		id, isMeta := transcript.SubagentMetaID(name)
		if entry.IsDir() || !isMeta {
			continue
		}
		tree.ids = append(tree.ids, id)
		meta, err := transcript.ReadSubagentMeta(filepath.Join(tree.dir, name))
		if err != nil {
			tree.err = err
			tree.warnings = append(tree.warnings, tree.err.Error())
			continue
		}
		if parent := strings.TrimSpace(meta.ParentAgentID); parent != "" && parent != id {
			tree.children[parent] = append(tree.children[parent], id)
		}
	}
	return tree
}

// judgeLiveness gives the tree what a transcript cannot say about an agent
// whose turn is open: the render's clock, and the tasks Claude Code itself
// lists as over (completed, failed, killed, error), whose open turn is a
// leftover, not work. Without it every open turn counts as work.
func (tree *agentTree) judgeLiveness(now time.Time, over map[string]bool) {
	tree.now, tree.over = now, over
}

// terminalStatus is a task status Claude Code gives a task that is over.
func terminalStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case taskCompleted, "failed", "killed", "error":
		return true
	}
	return false
}

// finishedTasks are the ids of every task in the payload Claude Code lists as
// over, rows it shows nothing for included: an agent it calls over is not
// working, whatever its transcript's last line says.
func finishedTasks(tasks []subagentTask) map[string]bool {
	over := map[string]bool{}
	for index := range tasks {
		if tasks[index].ID != "" && terminalStatus(tasks[index].Status) {
			over[tasks[index].ID] = true
		}
	}
	return over
}

// nesting counts every agent below one task, at any depth, and how many of
// them are working: their own turn is open. An orchestrator that ended its
// turn to wait on background workers is not working; its workers are.
func (tree *agentTree) nesting(id string) agentNesting {
	if tree.err != nil {
		return agentNesting{err: tree.err}
	}
	var nest agentNesting
	seen := map[string]bool{id: true}
	queue := append([]string(nil), tree.children[id]...)
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if seen[next] {
			continue
		}
		seen[next] = true
		queue = append(queue, tree.children[next]...)
		nest.total++
		switch tree.turn(next) {
		case turnWorking:
			nest.active++
		case turnUnread:
			nest.unknown++
		}
	}
	return nest
}

// drainWarnings hands back the causes gathered since the last call, so each
// is reported once, on the render that found it.
func (tree *agentTree) drainWarnings() []string {
	warnings := tree.warnings
	tree.warnings = nil
	return warnings
}

func (tree *agentTree) turn(id string) turnState {
	if state, ok := tree.open[id]; ok {
		return state
	}
	state := turnClosed
	open, last, err := turnOpen(transcript.SubagentTranscript(tree.dir, id))
	switch {
	case err != nil:
		tree.warnings = append(tree.warnings, err.Error())
		state = turnUnread
	case open && !tree.over[id] && (tree.now.IsZero() || tree.now.Sub(last) < agentDeadAfter):
		state = turnWorking
	}
	tree.open[id] = state
	return state
}

// turnOpen reads an agent transcript's last message entry: its turn is over
// only when that entry is an assistant message that stopped (a stop_reason)
// without a tool_use. A tool call, a tool result, a prompt or a message still
// streaming (no stop_reason yet) means it is working. A torn final line is the
// agent mid-write and fails to parse, so it is skipped; a transcript with no message entry yet is a
// turn that has not produced its first reply. last is when that entry was
// written, the file's mtime for a transcript with no message entry yet.
func turnOpen(path string) (open bool, last time.Time, err error) {
	file, err := os.Open(path)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("open sub-agent transcript %s: %w", path, err)
	}
	defer func() { _ = file.Close() }() // read-only handle; a close error loses nothing
	info, err := file.Stat()
	if err != nil {
		return false, time.Time{}, fmt.Errorf("stat sub-agent transcript %s: %w", path, err)
	}
	size := info.Size()
	for window := int64(tailWindow); ; window *= 2 {
		start := max(size-window, 0)
		chunk := make([]byte, size-start)
		if _, err := file.ReadAt(chunk, start); err != nil && !errors.Is(err, io.EOF) {
			return false, time.Time{}, fmt.Errorf("read sub-agent transcript %s: %w", path, err)
		}
		// The piece after the last newline stays: a torn one fails to parse
		// and is skipped, as readAgentActivity skips it.
		lines := bytes.Split(chunk, []byte{'\n'})
		if start > 0 && len(lines) > 0 {
			lines = lines[1:] // the window's first piece may start mid-line
		}
		for index := len(lines) - 1; index >= 0; index-- {
			if open, stamped, ok := messageTurnOpen(lines[index]); ok {
				if stamped.IsZero() {
					stamped = info.ModTime()
				}
				return open, stamped, nil
			}
		}
		if start == 0 {
			return true, info.ModTime(), nil
		}
	}
}

// messageTurnOpen answers for one transcript line; ok is false for a line
// that is not a user or assistant message. stamped is the line's timestamp,
// zero when it carries none.
func messageTurnOpen(line []byte) (open bool, stamped time.Time, ok bool) {
	var entry struct {
		Type      string    `json:"type"`
		Timestamp time.Time `json:"timestamp"`
		Message   struct {
			StopReason *string         `json:"stop_reason"`
			Content    json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return false, time.Time{}, false
	}
	switch entry.Type {
	case entryUser:
		return true, entry.Timestamp, true
	case entryAssistant:
	default:
		return false, time.Time{}, false
	}
	if entry.Message.StopReason == nil || *entry.Message.StopReason == "" {
		return true, entry.Timestamp, true
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(entry.Message.Content, &blocks) == nil {
		for _, block := range blocks {
			if block.Type == blockToolUse {
				return true, entry.Timestamp, true
			}
		}
	}
	return false, entry.Timestamp, true
}

// nestingSegment renders "2/5": the agents below the task working right now
// (green) over all of them at every depth (the tools colour). "solo" when it
// spawned none; "?/?" when the scan failed, and an "(N unread)" suffix for
// agents whose turn state could not be read.
func nestingSegment(nest agentNesting) string {
	if nest.err != nil {
		return cWarn + "?/?" + reset
	}
	if nest.total == 0 {
		return cMuted + "solo" + reset
	}
	working := cRunning + strconv.Itoa(nest.active) + reset
	line := working + cMuted + "/" + reset + cTools + strconv.Itoa(nest.total) + reset
	if nest.unknown > 0 {
		line += cWarn + " (" + strconv.Itoa(nest.unknown) + " unread)" + reset
	}
	return line
}
