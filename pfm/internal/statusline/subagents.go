package statusline

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/pyvalue"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// subagentTask is one running agent's row as its meta file names it
// (runningAgents): its id, the name, description, model and effort it was
// spawned with, its start, and the context window the render gives it.
type subagentTask struct {
	ID                string
	Name              string
	Label             string
	Model             string
	Effort            string
	StartTime         int64 // Unix milliseconds
	ContextWindowSize int64
	capacityState     string
}

// agentActivity is what a sub-agent's own files add to the payload: its role
// from agent-<id>.meta.json, the rest from agent-<id>.jsonl. cacheHit is -1
// until the first model turn lands; effort is the level its latest request
// went out at, "" until one records it; err means the transcript could not be
// read and roleErr the meta file, each rendering "?" — never as zero or empty.
// spend is its own billing plus every agent's below it. noCacheWindow is set
// for an agent whose provider names no cache lifetime (a gpt- model through the
// gateway's chatgpt route): its row shows the hit share and no countdown.
type agentActivity struct {
	role        string
	roleErr     error
	effort      string
	tools       int
	errors      int
	compactions int
	cacheHit    int
	model       string
	cwd         string
	transcript  string
	last        time.Time
	err         error
	nest        agentNesting
	spend       agentSpend
	// turn is its last message entry.
	turn agentTurn

	noCacheWindow bool
	context       int64 // newest completed call's last executor request: input + cache read + cache creation; 0 until one lands, and after a compaction
}

const (
	subagentBarWidth = 8
	taskRunning      = "running"
	entryAssistant   = "assistant"
	entryUser        = "user"
	entryCostState   = "cost-state"
	blockToolUse     = "tool_use"
	// stallAfter is the quiet time before a running agent's row says idle:
	// shorter gaps are ordinary model latency.
	stallAfter = time.Minute
	// rowSep joins a row's segments tight: a row holds many.
	rowSep = dim + "│" + reset
)

// Drop ranks, in the order a row too wide for the status line gives parts up: the
// cwd, the cache, the compactions, then the effort (the model
// family stays). Unranked parts — nesting, gauge and spend, identity, status,
// idle, errors, label — always stay; the label is last, so Claude Code's own
// truncation cuts only it.
const (
	_ = iota // 0: a part that always stays
	dropCwd
	dropCache
	dropCompactions
	dropEffort
)

// LoadServedPrices is the machine's served price table (pricing.LoadPrices:
// the clone file or the embedded copy, the override beside pfmConfigPath
// merged), never fetched. A table that cannot load is nil — every dollar then
// renders "$?" — and its cause goes to stderr under who, as does a clone file
// the embedded copy stood in for. An override that cannot be used is no reason
// to lose the table: the published one prices, and the override's cause goes
// to stderr.
func LoadServedPrices(home, pfmConfigPath string, stderr io.Writer, who string) *pricing.Prices {
	table, err := pricing.LoadPrices(home, pfmConfigPath)
	if err != nil {
		if table.FetchedAt == "" { // no published table came back to price by
			fmt.Fprintf(stderr, "%s: price table (dollars show $?): %v\n", who, err)
			return nil
		}
		fmt.Fprintf(stderr, "%s: price override unusable, pricing from the published table: %v\n", who, err)
	}
	if table.FileError != nil {
		fmt.Fprintf(
			stderr,
			"%s: clone prices unusable, pricing from the %s table: %v\n",
			who,
			table.From,
			table.FileError,
		)
	}
	return &table
}

// agentRowsInput is what a render of the agent rows reads beside each task:
// the session's transcript, id and directory, the width to fit, and the
// session's own model and window, which an agent on that model shares.
type agentRowsInput struct {
	SessionID      string
	TranscriptPath string
	Cwd            string
	Columns        int
	SessionModel   string
	SessionWindow  int64
	Tasks          []subagentTask
}

// renderAgentRows renders one row body per task, in order, even before its
// model or token counters arrive: unknown context stays visible as "?". live
// is every agent of the session that runs, rows not shown included: it decides
// which agents below a task count as working. A sub-agent transcript that
// cannot be read renders "?" in its row and its cause goes to warn. A nil
// prices leaves every row's dollars unpriced. A session with a cost ledger
// prices each row from it: the ledger rows of that agent and every agent below
// it (ledger_spend.go); an agent it holds no row of yet keeps its transcripts'
// price. A row's identity is the agentType it was launched as.
func renderAgentRows(
	data agentRowsInput,
	live map[string]bool,
	now time.Time,
	sidDir string,
	prices *pricing.Prices,
	home string,
	env paths.Env,
	scans transcriptScans,
	warn io.Writer,
) []string {
	inherited, err := inheritedEffort(sidDir, data.SessionID)
	if err != nil {
		fmt.Fprintf(warn, "%s: session effort (rows show model alone): %v\n", agentRowsWho, err)
	}
	catalog := newContextCatalog(home, env.Get, now, warn)
	tree := scanAgentTree(data.TranscriptPath, prices, scans)
	tree.judgeLiveness(live)
	ledger := loadLedgerView(data.SessionID, home, env, warn)
	rows := make([]string, 0, len(data.Tasks))
	for index := range data.Tasks {
		task := &data.Tasks[index]
		read := *tree.read(task.ID)
		if read.model != "" { // the model its calls went to; the spawn model is an alias or absent
			task.Model = read.model
		}
		read.noCacheWindow = openAIModel(task.Model)
		read.nest = tree.nesting(task.ID)
		read.spend.add(tree.nestedSpend(task.ID))
		if ledger != nil {
			if booked, ok := ledger.spend(task.ID, tree.children); ok && (booked.priced > 0 || read.err != nil) {
				// a ledger that prices nothing still counts the tokens of a row whose transcript did not read
				read.spend = read.spend.withLedger(booked)
			}
		}
		for _, cause := range tree.drainWarnings() {
			fmt.Fprintf(warn, "%s: row %s: nested agents: %s\n", agentRowsWho, task.ID, cause)
		}
		for index, err := range []error{read.roleErr, read.err} {
			if err != nil && (index == 0 || err != read.roleErr) {
				fmt.Fprintf(warn, "%s: row %s: %v\n", agentRowsWho, task.ID, err)
			}
		}
		switch {
		case openAIModel(task.Model):
			task.ContextWindowSize, task.capacityState = catalog.capacity(task.Model)
		case task.ContextWindowSize == 0 && task.Model != "" && modelVersion(task.Model) == modelVersion(data.SessionModel):
			task.ContextWindowSize = data.SessionWindow
		}
		rows = append(rows, subagentContent(task, &read, data.Cwd, data.Columns, inherited, now))
	}
	return rows
}

// subagentContent renders nested → gauge and spend → name·role → model·effort → running
// and time → idle → errors → cache → compactions → cwd → label, fitted to
// columns; the nested count (working/all) leads because it is what a parent
// row is read for, and the label goes last because a terminal truncates the
// row's tail. The cwd shows only when the agent works outside the session's
// own directory — the same one is noise.
func subagentContent(
	task *subagentTask,
	activity *agentActivity,
	sessionCwd string,
	columns int,
	inherited sessionEffortRecord,
	now time.Time,
) string {
	return fitRow(activeContent(task, activity, sessionCwd, inherited, now), columns)
}

// activeContent is the full row's parts: see subagentContent.
func activeContent(
	task *subagentTask,
	activity *agentActivity,
	sessionCwd string,
	inherited sessionEffortRecord,
	now time.Time,
) []rowPart {
	gauge := agentContextGauge(task, activity) + " " + spendSegment(*activity)
	parts := []rowPart{
		{text: nestingSegment(activity.nest)},
		{text: gauge},
		{text: subagentIdentity(task.Name, activity)},
		{
			text:  subagentModel(task.Model, activity.effort, task.Effort, inherited),
			short: subagentModel(task.Model, "", "", sessionEffortRecord{}),
			drop:  dropEffort,
		},
		{text: subagentStatus(task, now)},
	}
	parts = append(parts, activitySegments(*activity, now)...)
	cwd := strings.TrimSpace(activity.cwd)
	if cwd != "" && filepath.Clean(cwd) != filepath.Clean(strings.TrimSpace(sessionCwd)) {
		parts = append(parts, rowPart{text: cCwd + filepath.Base(cwd) + reset, drop: dropCwd})
	}
	if label := strings.TrimSpace(task.Label); label != "" {
		parts = append(parts, rowPart{text: cLabel + label + reset})
	}
	return parts
}

// subagentIdentity renders name·role: the agent's name when it was given one,
// and its agentType from the meta file; "role ?" when that file is unreadable.
func subagentIdentity(name string, activity *agentActivity) string {
	name = strings.TrimSpace(name)
	role := cRole + activity.role + reset
	if activity.roleErr != nil {
		role = cWarn + "role ?" + reset
	}
	if name == "" {
		return role
	}
	return cName + name + reset + cMuted + "·" + reset + role
}

// subagentStatus renders "running" and the time since the agent started.
func subagentStatus(task *subagentTask, now time.Time) string {
	segment := cRunning + taskRunning + reset
	if task.StartTime <= 0 {
		return segment
	}
	ran := time.Duration(max(now.UnixMilli()-task.StartTime, 0)) * time.Millisecond
	return segment + " " + cElapsed + formatCacheTime(ran, false) + reset
}

// activitySegments renders idle → errors → cache → compactions from
// the transcript; idle, errors and compactions appear only when they say
// something. The cache segment has the main line's shape: the time left on the
// agent's own prompt cache, then the share of its newest call's prompt read
// from it. A transcript that could not be read shows "tools ?" and "💾!".
func activitySegments(activity agentActivity, now time.Time) []rowPart {
	if activity.err != nil {
		return []rowPart{{text: cBad + "💾!" + reset}}
	}
	var parts []rowPart
	if quiet := now.Sub(activity.last); !activity.last.IsZero() && quiet >= stallAfter {
		color := cWarn
		if quiet >= 5*stallAfter {
			color = cBad
		}
		parts = append(parts, rowPart{text: color + "idle " + formatDuration(quiet.Milliseconds()) + reset})
	}
	if activity.errors > 0 {
		parts = append(parts, rowPart{text: cBad + plural(activity.errors, "error") + reset})
	}
	cache := agentCacheText(activity.transcript, activity.cacheHit, now)
	if activity.noCacheWindow {
		cache = unmeasuredCacheText(activity.cacheHit)
	}
	parts = append(parts, rowPart{text: cache, drop: dropCache})
	if activity.compactions > 0 {
		compactions := cCompaction + fmt.Sprintf("⟲%d", activity.compactions) + reset
		parts = append(parts, rowPart{text: compactions, drop: dropCompactions})
	}
	return parts
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// readAgentActivity reads the sub-agent's own files, which Claude Code keeps
// beside the session's: <session>/subagents/agent-<id>.{meta.json,jsonl}. It
// counts distinct tool_use blocks, errored tool results and compact
// boundaries, takes the cache hit and the effort from the newest assistant
// entry that carries each, the newest entry timestamp, and what its responses
// billed, folding the transcript from its "activity" checkpoint (an unfinished
// fold leaves floors and an unread turn). A torn final line — the agent is
// mid-write — is never kept in the checkpoint.
func readAgentActivity(sessionTranscript, id string, prices *pricing.Prices, scans transcriptScans) agentActivity {
	activity := agentActivity{cacheHit: -1}
	if strings.TrimSpace(sessionTranscript) == "" {
		activity.err = errors.New("payload names no session transcript")
		activity.roleErr = activity.err
		return activity
	}
	path := transcript.SubagentTranscript(transcript.SubagentsDir(sessionTranscript), id)
	activity.role, activity.roleErr = readAgentRole(transcript.SubagentMetaPath(path))
	activity.transcript = path
	fold := newActivityFold(prices)
	done, err := scans.fold(path, "activity", fold)
	if err != nil {
		var opened bool
		if opened, activity.err = transcriptError(path, err); !opened {
			return activity
		}
	}
	activity.errors, activity.compactions = fold.Errors, fold.Compactions
	activity.effort, activity.model, activity.cwd = fold.Effort, fold.Model, fold.Cwd
	activity.tools = len(fold.Seen)
	activity.spend = fold.Spend.total()
	activity.spend.tools = activity.tools
	if err == nil && !done { // counts and spend are floors; the newest entry, turn, context and hit unknown
		activity.spend.partial, activity.spend.behind = true, true
		activity.turn.err = fmt.Errorf("sub-agent transcript %s not read to its end within the budget", path)
		return activity
	}
	activity.last, activity.cacheHit, activity.context = fold.Last, fold.CacheHit, fold.Context
	// No message entry yet: the first turn is under way.
	activity.turn = agentTurn{open: fold.Turn.Open || !fold.Turn.Seen, seen: fold.Turn.Seen}
	return activity
}

// readAgentRole reads agentType from the sub-agent's meta file.
func readAgentRole(path string) (string, error) {
	meta, err := transcript.ReadSubagentMeta(path)
	if err != nil {
		return "", err
	}
	agentType := strings.TrimSpace(meta.AgentType)
	if agentType == "" {
		return "", fmt.Errorf("sub-agent meta %s names no agentType", path)
	}
	return agentType, nil
}

func recordTranscriptLine(line []byte, fold *activityFold) {
	var entry struct {
		Type      string    `json:"type"`
		Subtype   string    `json:"subtype"`
		RequestID string    `json:"requestId"`
		Timestamp time.Time `json:"timestamp"`
		Effort    any       `json:"effort"`
		Cwd       string    `json:"cwd"`
		Message   struct {
			ID      string           `json:"id"`
			Model   string           `json:"model"`
			Content json.RawMessage  `json:"content"`
			Usage   *transcriptUsage `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return
	}
	if entry.Timestamp.After(fold.Last) {
		fold.Last = entry.Timestamp
	}
	if cwd := strings.TrimSpace(entry.Cwd); cwd != "" {
		fold.Cwd = cwd
	}
	if entry.Type == "system" && entry.Subtype == "compact_boundary" {
		fold.Compactions++
		fold.Context = 0 // a call from before the boundary no longer describes the context
		return
	}
	if entry.Type != entryAssistant && entry.Type != entryUser {
		return
	}
	blocks, _ := transcript.DecodeContent(entry.Message.Content)
	for index := range blocks {
		block := &blocks[index]
		id, _ := pyvalue.String(block.ID)
		switch {
		case entry.Type == entryAssistant && block.Kind() == blockToolUse && id != "":
			fold.Seen[id] = struct{}{}
		case entry.Type == entryUser && block.Kind() == "tool_result" && string(block.IsError) == "true":
			fold.Errors++
		}
	}
	if entry.Type != entryAssistant {
		return
	}
	// The level the request went out at; a model that takes none leaves it out.
	if level, ok := entry.Effort.(string); ok && strings.TrimSpace(level) != "" {
		fold.Effort = strings.TrimSpace(level)
	}
	if model := entry.Message.Model; model != "" && model != "<synthetic>" {
		fold.Model = model
	}
	usage := entry.Message.Usage
	if usage == nil {
		return
	}
	fold.Spend.record(
		entry.Message.ID,
		entry.RequestID,
		entry.Message.Model,
		usage,
		entry.Timestamp.Format(time.RFC3339Nano),
	)
	// A line that reports no cache counters is a call still streaming (the
	// gateway's message_start carries its whole-prompt estimate in
	// input_tokens and nothing else): reading it as a call would drop the
	// newest completed call's share to 0%.
	if usage.reportsCache {
		if hit := cacheHitPercent(usage.CacheRead, usage.CacheCreation, usage.Input); hit >= 0 {
			fold.CacheHit = hit
		}
	}
	// The context is the prompt of the response's last executor request, as the
	// main session's gauge reads it: a response that ran several requests
	// carries their sum at the top level, or only zeros there and its counts in
	// usage.iterations. Iterations mean the call completed even without cache
	// counters at the top level.
	if usage.reportsCache || len(usage.Iterations) > 0 {
		if requests := usage.Requests(); len(requests) > 0 {
			if size := requests[len(requests)-1].Context(); size > 0 {
				fold.Context = size
			}
		}
	}
}
