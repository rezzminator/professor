package statusline

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/pricing"
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
// each agent's own turn state for the render, reads each agent's one pass over
// its transcript (activity, billing and turn state alike).
// over and now (judgeLiveness) decide which agents with an open turn really
// work: a transcript cannot say an agent was killed.
type agentTree struct {
	dir      string
	children map[string][]string
	err      error
	warnings []string
	open     map[string]turnState
	reads    map[string]*agentActivity
	warned   map[string]bool
	session  string
	prices   *pricing.Prices
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

// scanAgentTree reads every agent-<id>.meta.json beside the session's
// transcript once per render. A directory that cannot be listed, or any meta
// file that cannot be read, sets err: the parent of that agent is unknown, so
// no row may claim a count it cannot vouch for.
func scanAgentTree(sessionTranscript string, prices *pricing.Prices) *agentTree {
	tree := &agentTree{
		children: map[string][]string{}, open: map[string]turnState{}, reads: map[string]*agentActivity{},
		warned: map[string]bool{}, session: sessionTranscript, prices: prices,
	}
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

// read is the agent's one pass over its transcript for the render: whoever
// asks first (its task row, a count above it, a spend above it) reads it, and
// everyone after gets the same result.
func (tree *agentTree) read(id string) *agentActivity {
	if activity, ok := tree.reads[id]; ok {
		return activity
	}
	activity := readAgentActivity(tree.session, id, tree.prices)
	tree.reads[id] = &activity
	return &activity
}

// warnOnce reports an agent's unreadable transcript on the first render step
// that depends on it.
func (tree *agentTree) warnOnce(id string, err error) {
	if err != nil && !tree.warned[id] {
		tree.warned[id] = true
		tree.warnings = append(tree.warnings, err.Error())
	}
}

func (tree *agentTree) turn(id string) turnState {
	if state, ok := tree.open[id]; ok {
		return state
	}
	state := turnClosed
	// Asked again while its own answer is being worked out (a parentAgentId
	// cycle the liveness walk goes around), it reads closed: it is not yet
	// known to work, and the walk must end.
	tree.open[id] = state
	activity := tree.read(id)
	switch {
	case activity.err != nil:
		tree.warnOnce(id, activity.err)
		state = turnUnread
	case activity.turn.err != nil:
		tree.warnOnce(id, activity.turn.err)
		state = turnUnread
	case tree.working(id, activity):
		state = turnWorking
	}
	tree.open[id] = state
	return state
}

// working says whether an agent whose transcript was read is working now. An
// open turn counts while it is recent, never for a turn the task list calls
// over. A turn whose last entry is a call to a sub-agent counts however long
// it has been quiet while any agent it spawned works: it waits on that child,
// and the child's own turn is what ends or outlives the wait.
func (tree *agentTree) working(id string, activity *agentActivity) bool {
	if !activity.turn.open || tree.over[id] {
		return false
	}
	if tree.now.IsZero() || tree.now.Sub(activity.turn.last) < agentDeadAfter {
		return true
	}
	if !activity.turn.delegating {
		return false
	}
	return tree.childWorking(id, map[string]bool{id: true})
}

func (tree *agentTree) childWorking(id string, seen map[string]bool) bool {
	for _, child := range tree.children[id] {
		if seen[child] {
			continue
		}
		seen[child] = true
		if tree.turn(child) == turnWorking {
			return true
		}
	}
	return false
}

// agentTurn is an agent transcript's last message entry: its turn is over when
// that entry is an assistant message that stopped (a stop_reason) without a
// tool_use, or a tool result Claude Code marks toolEndsTurn (the result of
// SubagentHandback, the tool a sub-agent ends its turn with, or of
// StructuredOutput in a workflow agent: nothing follows it). Any other tool
// call, tool result or prompt, or a message still streaming (no stop_reason
// yet), means it is working; a transcript from a Claude Code that writes no
// toolEndsTurn reads as before. A transcript with no
// message entry yet is a turn that has not produced its first reply. last is
// when that entry was written, the file's mtime when it carries no timestamp.
// delegating is an open turn whose entry calls a sub-agent, stop_reason
// written or not. err is why last could not be read (the transcript went
// between its read and its stat): only the turn's state is then unknown.
type agentTurn struct {
	open       bool
	last       time.Time
	delegating bool
	seen       bool
	err        error
}

// stampTurn dates a last message entry that carries no timestamp by the
// transcript's mtime. A stat failing after the read leaves the row's read
// facts standing: only the turn's state is unknown.
func (activity *agentActivity) stampTurn(path string) {
	info, err := os.Stat(path)
	if err != nil {
		activity.turn.err = fmt.Errorf("stat sub-agent transcript %s: %w", path, err)
		return
	}
	activity.turn.last = info.ModTime()
}

// messageTurnOpen answers for one transcript line; ok is false for a line
// that is not a user or assistant message. stamped is the line's timestamp,
// zero when it carries none.
func messageTurnOpen(line []byte) (open, delegating bool, stamped time.Time, ok bool) {
	var entry struct {
		Type         string    `json:"type"`
		Timestamp    time.Time `json:"timestamp"`
		ToolEndsTurn bool      `json:"toolEndsTurn"`
		Message      struct {
			StopReason *string         `json:"stop_reason"`
			Content    json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return false, false, time.Time{}, false
	}
	switch entry.Type {
	case entryUser:
		return !entry.ToolEndsTurn, false, entry.Timestamp, true
	case entryAssistant:
	default:
		return false, false, time.Time{}, false
	}
	var blocks []struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	toolCall := false
	if json.Unmarshal(entry.Message.Content, &blocks) == nil {
		for _, block := range blocks {
			if block.Type == blockToolUse {
				toolCall = true
				delegating = delegating || subagentCallTools[block.Name]
			}
		}
	}
	if toolCall || entry.Message.StopReason == nil || *entry.Message.StopReason == "" {
		return true, delegating, entry.Timestamp, true
	}
	return false, false, entry.Timestamp, true
}

// subagentCallTools are the tool names that start a sub-agent.
var subagentCallTools = map[string]bool{"Agent": true, "Task": true}

// nestingSegment renders "2/5": the agents below the task working right now
// (green) over all of them at every depth (the tools colour). "" when it
// spawned none, so the row carries no segment and no separator; "?/?" when the
// scan failed, and an "(N unread)" suffix for agents whose turn state could
// not be read.
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
