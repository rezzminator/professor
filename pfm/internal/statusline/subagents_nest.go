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
// each agent's own turn state for the render.
type agentTree struct {
	dir      string
	children map[string][]string
	err      error
	warnings []string
	open     map[string]turnState
}

type turnState int

const (
	turnUnread turnState = iota
	turnClosed
	turnWorking
)

// tailWindow is the first read from a transcript's end; it doubles until a
// message entry is found, so one huge tool result costs a longer read, never
// a wrong answer.
const tailWindow = 64 << 10

// scanAgentTree reads every agent-<id>.meta.json beside the session's
// transcript once per render. A directory that cannot be listed, or any meta
// file that cannot be read, sets err: the parent of that agent is unknown, so
// no row may claim a count it cannot vouch for.
func scanAgentTree(sessionTranscript string) *agentTree {
	tree := &agentTree{children: map[string][]string{}, open: map[string]turnState{}}
	if strings.TrimSpace(sessionTranscript) == "" {
		tree.err = errors.New("payload names no session transcript")
		return tree
	}
	tree.dir = filepath.Join(strings.TrimSuffix(sessionTranscript, ".jsonl"), "subagents")
	entries, err := os.ReadDir(tree.dir)
	if err != nil {
		tree.err = fmt.Errorf("list %s: %w", tree.dir, err)
		tree.warnings = append(tree.warnings, tree.err.Error())
		return tree
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".meta.json")
		path := filepath.Join(tree.dir, name)
		raw, err := os.ReadFile(path)
		if err == nil {
			var meta struct {
				ParentAgentID string `json:"parentAgentId"`
			}
			if err = json.Unmarshal(raw, &meta); err == nil {
				if parent := strings.TrimSpace(meta.ParentAgentID); parent != "" && parent != id {
					tree.children[parent] = append(tree.children[parent], id)
				}
				continue
			}
		}
		tree.err = fmt.Errorf("read sub-agent meta %s: %w", path, err)
		tree.warnings = append(tree.warnings, tree.err.Error())
	}
	return tree
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
	open, err := turnOpen(filepath.Join(tree.dir, "agent-"+id+".jsonl"))
	switch {
	case err != nil:
		tree.warnings = append(tree.warnings, err.Error())
		state = turnUnread
	case open:
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
// turn that has not produced its first reply.
func turnOpen(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open sub-agent transcript %s: %w", path, err)
	}
	defer func() { _ = file.Close() }() // read-only handle; a close error loses nothing
	info, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("stat sub-agent transcript %s: %w", path, err)
	}
	size := info.Size()
	for window := int64(tailWindow); ; window *= 2 {
		start := max(size-window, 0)
		chunk := make([]byte, size-start)
		if _, err := file.ReadAt(chunk, start); err != nil && !errors.Is(err, io.EOF) {
			return false, fmt.Errorf("read sub-agent transcript %s: %w", path, err)
		}
		// The piece after the last newline stays: a torn one fails to parse
		// and is skipped, as readAgentActivity skips it.
		lines := bytes.Split(chunk, []byte{'\n'})
		if start > 0 && len(lines) > 0 {
			lines = lines[1:] // the window's first piece may start mid-line
		}
		for index := len(lines) - 1; index >= 0; index-- {
			if open, ok := messageTurnOpen(lines[index]); ok {
				return open, nil
			}
		}
		if start == 0 {
			return true, nil
		}
	}
}

// messageTurnOpen answers for one transcript line; ok is false for a line
// that is not a user or assistant message.
func messageTurnOpen(line []byte) (open, ok bool) {
	var entry struct {
		Type    string `json:"type"`
		Message struct {
			StopReason *string         `json:"stop_reason"`
			Content    json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return false, false
	}
	switch entry.Type {
	case entryUser:
		return true, true
	case entryAssistant:
	default:
		return false, false
	}
	if entry.Message.StopReason == nil || *entry.Message.StopReason == "" {
		return true, true
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(entry.Message.Content, &blocks) == nil {
		for _, block := range blocks {
			if block.Type == "tool_use" {
				return true, true
			}
		}
	}
	return false, true
}

// nestingSegment renders "2/5": the agents below the task working right now
// (green) over all of them at every depth (the tools colour). Absent when it
// spawned none; "?/?" when the scan failed, and an "(N unread)" suffix for
// agents whose turn state could not be read.
func nestingSegment(nest agentNesting) string {
	if nest.err != nil {
		return cWarn + "?/?" + reset
	}
	if nest.total == 0 {
		return ""
	}
	working := cRunning + strconv.Itoa(nest.active) + reset
	line := working + cMuted + "/" + reset + cTools + strconv.Itoa(nest.total) + reset
	if nest.unknown > 0 {
		line += cWarn + " (" + strconv.Itoa(nest.unknown) + " unread)" + reset
	}
	return line
}
