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
// depth: how many there are, how many are working right now (they still run,
// runningAgents, and their own turn is open), and how many could
// not be read, an agent whose meta file could not be read included (its parent
// is unknown, so it may sit below the task). err is a failure to look (the
// subagents directory could not be listed) and renders "?/?", never as "none".
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
// live (judgeLiveness) decides which agents with an open turn really work: a
// transcript cannot say an agent finished mid-stream or was killed.
type agentTree struct {
	dir      string
	children map[string][]string
	err      error
	warnings []string
	// unreadMeta are the agents whose meta file could not be read: their parent
	// is unknown, every other edge stands.
	unreadMeta []string
	open       map[string]turnState
	reads      map[string]*agentActivity
	warned     map[string]bool
	session    string
	prices     *pricing.Prices
	scans      transcriptScans
	ids        []string
	live       map[string]bool
}

type turnState int

const (
	turnUnread turnState = iota
	turnClosed
	turnWorking
)

// scanAgentTree reads every agent-<id>.meta.json beside the session's
// transcript once per render. A directory that cannot be listed sets err: no
// row may claim a count it cannot vouch for. A meta file that cannot be read
// costs only that agent's parent (unreadMeta) and warns; every other edge stands.
func scanAgentTree(sessionTranscript string, prices *pricing.Prices, scans transcriptScans) *agentTree {
	tree := &agentTree{
		children: map[string][]string{}, open: map[string]turnState{}, reads: map[string]*agentActivity{},
		warned: map[string]bool{}, session: sessionTranscript, prices: prices, scans: scans,
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
			tree.unreadMeta = append(tree.unreadMeta, id)
			tree.warnings = append(tree.warnings, err.Error())
			continue
		}
		if parent := strings.TrimSpace(meta.ParentAgentID); parent != "" && parent != id {
			tree.children[parent] = append(tree.children[parent], id)
		}
	}
	return tree
}

// judgeLiveness gives the tree what a transcript cannot say about an agent
// whose turn is open: the agents that still run (runningAgents). An
// agent outside that set is not working, whatever its transcript's last line
// says; one inside it with an open turn works, however long it has been quiet
// (a request Claude Code still waits on writes nothing). Without it every open
// turn counts as work.
func (tree *agentTree) judgeLiveness(live map[string]bool) {
	tree.live = live
}

// nesting counts every agent below one task, at any depth, and how many of
// them are working: Claude Code runs them and their own turn is open. An
// orchestrator that ended its turn to wait on background workers is not
// working; its workers are.
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
	nest.unknown += len(tree.unreadMeta)
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
	activity := readAgentActivity(tree.session, id, tree.prices, tree.scans)
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
	if tree.live != nil && !tree.live[id] {
		// Claude Code no longer runs it: no transcript read can make it work.
		tree.open[id] = state
		return state
	}
	activity := tree.read(id)
	switch {
	case activity.err != nil:
		tree.warnOnce(id, activity.err)
		state = turnUnread
	case activity.turn.err != nil:
		tree.warnOnce(id, activity.turn.err)
		state = turnUnread
	case activity.turn.open:
		state = turnWorking
	}
	tree.open[id] = state
	return state
}

// agentTurn is an agent transcript's last message entry: its turn is over when
// that entry is an assistant message that stopped (a stop_reason) without a
// tool_use, or a tool result Claude Code marks toolEndsTurn (the result of
// SubagentHandback, the tool a sub-agent ends its turn with, or of
// StructuredOutput in a workflow agent: nothing follows it). Any other tool
// call, tool result or prompt, or a message still streaming (no stop_reason
// yet), means it is working; a transcript from a Claude Code that writes no
// toolEndsTurn reads as before. A transcript with no
// message entry yet is a turn that has not produced its first reply. err is
// why the turn could not be read (the fold did not reach the transcript's
// end): only the turn's state is then unknown.
type agentTurn struct {
	open bool
	seen bool
	err  error
}

// activityFold is what readAgentActivity folds from a sub-agent transcript;
// its exported fields are the whole fold, which a scan checkpoint keeps.
type activityFold struct {
	Seen        map[string]struct{} `json:"seen"`
	Last        time.Time           `json:"last"`
	Errors      int                 `json:"errors"`
	Compactions int                 `json:"compactions"`
	CacheHit    int                 `json:"cacheHit"`
	Context     int64               `json:"context"`
	Effort      string              `json:"effort,omitempty"`
	Model       string              `json:"model,omitempty"`
	Cwd         string              `json:"cwd,omitempty"`
	Turn        turnFold            `json:"turn"`
	Spend       *spendScan          `json:"spend"`
	// ownSpend is the scan Spend must be: a checkpoint decodes into it, and one
	// that dropped it is refused (loaded).
	ownSpend *spendScan
}

// turnFold is the agent's last message entry (messageTurnOpen).
type turnFold struct {
	Open bool `json:"open"`
	Seen bool `json:"seen"`
}

// newActivityFold folds a sub-agent transcript, pricing its spend at prices.
func newActivityFold(prices *pricing.Prices) *activityFold {
	spend := newSpendScan(prices)
	return &activityFold{Seen: map[string]struct{}{}, CacheHit: -1, Spend: spend, ownSpend: spend}
}

func (fold *activityFold) reset() {
	fold.ownSpend.reset()
	*fold = activityFold{Seen: map[string]struct{}{}, CacheHit: -1, Spend: fold.ownSpend, ownSpend: fold.ownSpend}
}

func (fold *activityFold) loaded() error {
	if fold.Seen == nil || fold.Spend != fold.ownSpend {
		return errors.New("activity fold without its tool calls or spend")
	}
	return fold.Spend.loaded()
}

func (fold *activityFold) line(line []byte) {
	recordTranscriptLine(line, fold)
	if open, ok := messageTurnOpen(line); ok {
		fold.Turn = turnFold{Open: open, Seen: true}
	}
}

// messageTurnOpen answers for one transcript line; ok is false for a line
// that is not a user or assistant message.
func messageTurnOpen(line []byte) (open, ok bool) {
	var entry struct {
		Type         string `json:"type"`
		ToolEndsTurn bool   `json:"toolEndsTurn"`
		Message      struct {
			StopReason *string         `json:"stop_reason"`
			Content    json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return false, false
	}
	switch entry.Type {
	case entryUser:
		return !entry.ToolEndsTurn, true
	case entryAssistant:
	default:
		return false, false
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	toolCall := false
	if json.Unmarshal(entry.Message.Content, &blocks) == nil {
		for _, block := range blocks {
			toolCall = toolCall || block.Type == blockToolUse
		}
	}
	return toolCall || entry.Message.StopReason == nil || *entry.Message.StopReason == "", true
}

// nestingSegment renders "2/5": the agents below the task working right now
// (green) over all of them at every depth (the tools colour). "" when it
// spawned none, so the row carries no segment and no separator; "?/?" when the
// scan failed, and an "(N unread)" suffix for agents whose turn state could
// not be read.
func nestingSegment(nest agentNesting) string {
	if nest.err != nil {
		return cWarn + "?/?" + reset
	}
	if nest.total == 0 && nest.unknown == 0 {
		return ""
	}
	working := cRunning + strconv.Itoa(nest.active) + reset
	line := working + cMuted + "/" + reset + cTools + strconv.Itoa(nest.total) + reset
	if nest.unknown > 0 {
		line += cWarn + " (" + strconv.Itoa(nest.unknown) + " unread)" + reset
	}
	return line
}
