package statusline

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/modelglyph"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// subagentInput is the row context Claude Code's subagentStatusLine setting
// writes to stdin on every agent-panel tick: one entry per task row it shows.
type subagentInput struct {
	SessionID      string         `json:"session_id"`
	TranscriptPath string         `json:"transcript_path"`
	Cwd            string         `json:"cwd"`
	Columns        int            `json:"columns"`
	Tasks          []subagentTask `json:"tasks"`
}

type subagentTask struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Type              string          `json:"type"`
	Status            string          `json:"status"`
	Label             string          `json:"label"`
	Description       string          `json:"description"`
	Model             string          `json:"model"`
	Effort            json.RawMessage `json:"effort"`
	StartTime         int64           `json:"startTime"`
	Cwd               string          `json:"cwd"`
	ContextWindowSize int64           `json:"contextWindowSize"`
	TokenCount        int64           `json:"tokenCount"`
}

// subagentRow is one line Claude Code accepts back: the task id and the body
// it renders in that task's agent-panel row.
type subagentRow struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

// agentActivity is what a sub-agent's own files add to the payload: its role
// from agent-<id>.meta.json, the rest from agent-<id>.jsonl. cacheHit is -1
// until the first model turn lands; effort is the level its latest request
// went out at, "" until one records it; err means the transcript could not be
// read and roleErr the meta file, each rendering "?" — never as zero or empty.
// spend is its own billing plus every agent's below it.
type agentActivity struct {
	role        string
	roleErr     error
	effort      string
	tools       int
	errors      int
	compactions int
	cacheHit    int
	transcript  string
	last        time.Time
	err         error
	nest        agentNesting
	spend       agentSpend
}

const (
	subagentBarWidth = 8
	localAgentTask   = "local_agent"
	taskRunning      = "running"
	taskCompleted    = "completed"
	// taskDelegating is pfm's status, not Claude Code's: the task stopped
	// while an agent it spawned, at any depth, still works.
	taskDelegating = "delegating"
	// finishedCollapse is how long a finished row keeps its full body before
	// it collapses to status, age, identity and label.
	finishedCollapse = time.Minute
	entryAssistant   = "assistant"
	entryUser        = "user"
	blockToolUse     = "tool_use"
	// stallAfter is the quiet time before a running agent's row says idle:
	// shorter gaps are ordinary model latency.
	stallAfter = time.Minute
	// rowSep joins a row's segments tight: the panel is narrow.
	rowSep = dim + "│" + reset
	// rowPrefixWidth is what Claude Code draws before a row body ("  ❯ ⏺ "),
	// measured on a live 125-column pane, plus a two-column margin.
	rowPrefixWidth = 8
)

// Drop ranks, in the order a row too wide for its panel gives parts up: the
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

// rowPart is one segment of a full row; short is what it becomes once its
// drop rank is reached ("" removes it).
type rowPart struct {
	text, short string
	drop        int
}

func appendRowSegment(line, segment string) string {
	switch {
	case segment == "":
		return line
	case line == "":
		return segment
	}
	return line + rowSep + segment
}

// fitRow joins the parts and, while the row is wider than columns leaves for
// it, gives parts up rank by rank. columns 0 (no width in the payload) keeps
// the full row.
func fitRow(parts []rowPart, columns int) string {
	join := func() string {
		line := ""
		for _, part := range parts {
			line = appendRowSegment(line, part.text)
		}
		return line
	}
	line := join()
	budget := columns - rowPrefixWidth
	for rank := dropCwd; columns > 0 && rank <= dropEffort && ansi.StringWidth(line) > budget; rank++ {
		for index := range parts {
			if parts[index].drop == rank {
				parts[index].text = parts[index].short
			}
		}
		line = join()
	}
	return line
}

// ansiSGR matches one colour escape: a muted row drops them all and wears one.
var ansiSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// ServeSubagents answers Claude Code's subagentStatusLine command: stdin's
// row context in, one {id,content} JSON line per agent-panel row out.
// Fail-open like the main line — any error prints nothing and exits 0, so
// every row keeps Claude Code's own body instead of the harness logging a
// failed command each tick. The price table is the machine's served one
// (pricing.LoadPrices: the clone file or the embedded copy, the override beside
// pfmConfigPath merged), never fetched; one that cannot load leaves every
// row's dollars "$?", its cause on stderr.
func ServeSubagents(stdin io.Reader, stdout, stderr io.Writer, machine paths.Values, pfmConfigPath string) int {
	raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		fmt.Fprintf(stderr, "pfm statusline --subagents: read input (fail-open): %v\n", err)
		return 0
	}
	var prices *pricing.Prices
	if table, err := pricing.LoadPrices(machine.Home, pfmConfigPath); err != nil {
		fmt.Fprintf(stderr, "pfm statusline --subagents: price table (rows show $?): %v\n", err)
	} else {
		prices = &table
		if table.FileError != nil {
			fmt.Fprintf(stderr, "pfm statusline --subagents: clone prices unusable, pricing from the %s table: %v\n",
				table.From, table.FileError)
		}
	}
	rows, err := RenderSubagents(raw, clock.Real.Now(), machine.SIDDir, prices, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "pfm statusline --subagents: render (fail-open): %v\n", err)
		return 0
	}
	if _, err := io.WriteString(stdout, rows); err != nil {
		fmt.Fprintf(stderr, "pfm statusline --subagents: write output (fail-open): %v\n", err)
	}
	return 0
}

// RenderSubagents turns one subagentStatusLine payload into the JSON lines
// Claude Code reads back, one per task that reports a model or a token count;
// a task it omits keeps Claude Code's own row body. A sub-agent transcript
// that cannot be read renders "?" in its row and its cause goes to warn. A nil
// prices leaves every row's dollars unpriced.
func RenderSubagents(raw []byte, now time.Time, sidDir string, prices *pricing.Prices, warn io.Writer) (string, error) {
	var data subagentInput
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", fmt.Errorf("parse subagent row context: %w", err)
	}
	inherited, err := inheritedEffort(sidDir, data.SessionID)
	if err != nil {
		fmt.Fprintf(warn, "pfm statusline --subagents: session effort (rows show model alone): %v\n", err)
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	var tree *agentTree // scanned once per render, on the first agent row
	for index := range data.Tasks {
		task := &data.Tasks[index]
		if task.ID == "" || (task.TokenCount <= 0 && task.ContextWindowSize <= 0) {
			continue
		}
		var activity *agentActivity
		if task.Type == localAgentTask {
			if tree == nil {
				tree = scanAgentTree(data.TranscriptPath)
			}
			read := readAgentActivity(data.TranscriptPath, task.ID, prices)
			read.nest = tree.nesting(task.ID)
			read.spend.add(tree.nestedSpend(task.ID, prices))
			for _, cause := range tree.drainWarnings() {
				fmt.Fprintf(warn, "pfm statusline --subagents: row %s: nested agents: %s\n", task.ID, cause)
			}
			for index, err := range []error{read.roleErr, read.err} {
				if err != nil && (index == 0 || err != read.roleErr) {
					fmt.Fprintf(warn, "pfm statusline --subagents: row %s: %v\n", task.ID, err)
				}
			}
			activity = &read
		}
		row := subagentRow{
			ID:      task.ID,
			Content: subagentContent(task, activity, data.Cwd, data.Columns, inherited, now),
		}
		if err := encoder.Encode(row); err != nil {
			return "", fmt.Errorf("encode subagent row %s: %w", task.ID, err)
		}
	}
	return out.String(), nil
}

// subagentContent renders nested → gauge and spend → name·role → model·effort → status
// and time → idle → tools → errors → cache → compactions → cwd → label; the
// nested count (working/all) leads because it is what a parent row is read for, and the
// label goes last because Claude Code truncates the row's tail.
// The cwd shows only when the agent works outside the session's own
// directory — the same one is noise. A finished row steps back: completed, it
// renders muted under Claude Code's own faint for its first minute; after
// that minute every finished row collapses to its status, how long ago it
// ended, its identity and its label. Claude Code, not this command, decides
// when the row leaves the panel.
func subagentContent(
	task *subagentTask,
	activity *agentActivity,
	sessionCwd string,
	columns int,
	inherited sessionEffortRecord,
	now time.Time,
) string {
	line := fitRow(activeContent(task, activity, sessionCwd, inherited, now), columns)
	if !rowFinished(task, activity) {
		return rowOpen + line
	}
	ended := time.Time{}
	if activity != nil {
		ended = activity.last
	}
	status := strings.TrimSpace(task.Status)
	if ended.IsZero() || now.Sub(ended) < finishedCollapse {
		if status != taskCompleted {
			return rowOpen + line // a failure keeps its full colour for its first minute: it is an alert
		}
		return cMuted + ansiSGR.ReplaceAllString(line, "") + reset
	}
	statusColor := cMuted
	if status != taskCompleted {
		statusColor = cFailed
	}
	ago := formatDuration(now.Sub(ended).Milliseconds())
	collapsed := statusColor + status + reset + cMuted + " " + ago + " ago" + reset
	if activity != nil {
		collapsed = appendRowSegment(collapsed, cMuted+ansiSGR.ReplaceAllString(spendSegment(*activity), "")+reset)
	}
	if identity := ansiSGR.ReplaceAllString(subagentIdentity(task.Name, activity), ""); identity != "" {
		collapsed = appendRowSegment(collapsed, cMuted+identity+reset)
	}
	if label := rowLabel(task); label != "" {
		collapsed = appendRowSegment(collapsed, cMuted+label+reset)
	}
	return collapsed
}

// rowFinished is true once the task stopped and no agent below it still runs:
// Claude Code marks an orchestrator completed while its background workers
// work on, and that row is not finished.
func rowFinished(task *subagentTask, activity *agentActivity) bool {
	switch strings.TrimSpace(task.Status) {
	case taskCompleted, "failed", "killed", "error":
	default:
		return false
	}
	return activity == nil || activity.nest.active == 0 && activity.nest.unknown == 0 && activity.nest.err == nil
}

func rowLabel(task *subagentTask) string {
	if label := strings.TrimSpace(task.Label); label != "" {
		return label
	}
	return strings.TrimSpace(task.Description)
}

// activeContent is the full row's parts, without its opening: see
// subagentContent.
func activeContent(
	task *subagentTask,
	activity *agentActivity,
	sessionCwd string,
	inherited sessionEffortRecord,
	now time.Time,
) []rowPart {
	gauge := cTokens + formatContextTokens(task.TokenCount) + reset
	if task.ContextWindowSize > 0 {
		percent := int(task.TokenCount * 100 / task.ContextWindowSize)
		gauge = makeBar(percent, subagentBarWidth) + " " +
			percentColor(percent) + fmt.Sprintf("%d%%", percent) + reset + " " +
			gauge + cWindow + "/" + formatContextTokens(task.ContextWindowSize) + reset
	}
	var parts []rowPart
	if activity != nil {
		parts = append(parts, rowPart{text: nestingSegment(activity.nest)})
		gauge += " " + spendSegment(*activity)
	}
	parts = append(parts, rowPart{text: gauge}, rowPart{text: subagentIdentity(task.Name, activity)})
	recorded := ""
	if activity != nil {
		recorded = activity.effort
	}
	parts = append(parts,
		rowPart{
			text:  subagentModel(task.Model, recorded, task.Effort, inherited),
			short: subagentModel(task.Model, "", nil, sessionEffortRecord{}),
			drop:  dropEffort,
		},
		rowPart{text: subagentStatus(task, activity, now)})
	if activity != nil {
		parts = append(parts, activitySegments(*activity, task.Status == taskRunning, now)...)
	}
	cwd := strings.TrimSpace(task.Cwd)
	if cwd != "" && filepath.Clean(cwd) != filepath.Clean(strings.TrimSpace(sessionCwd)) {
		parts = append(parts, rowPart{text: cCwd + filepath.Base(cwd) + reset, drop: dropCwd})
	}
	if label := rowLabel(task); label != "" {
		parts = append(parts, rowPart{text: cLabel + label + reset})
	}
	return parts
}

// subagentIdentity renders name·role: the agent's name when it was given one,
// and its agentType from the meta file; "role ?" when that file is unreadable.
func subagentIdentity(name string, activity *agentActivity) string {
	name = strings.TrimSpace(name)
	role := ""
	if activity != nil {
		role = cRole + activity.role + reset
		if activity.roleErr != nil {
			role = cWarn + "role ?" + reset
		}
	}
	switch {
	case name == "":
		return role
	case role == "":
		return cName + name + reset
	default:
		return cName + name + reset + cMuted + "·" + reset + role
	}
}

// subagentModel names the family and effort: claude-opus-5-5[1m] + high →
// opus·🏎️ high. The level its transcript recorded for its latest request
// (recorded) wins: it is the effort that request went out at, after any
// plugin pinned it, so it renders plainly on any model. Without one, the
// payload's effort shows; one that is not a JSON string is left out. A row
// with neither shows the session's recorded effort (inherited), exactly as
// a payload effort on the model it was recorded for and muted on another,
// where Claude Code may resolve a different level.
func subagentModel(model, recorded string, effort json.RawMessage, inherited sessionEffortRecord) string {
	family := modelFamily(model)
	level := strings.TrimSpace(recorded)
	color := cEffort
	if level == "" &&
		(len(effort) == 0 || json.Unmarshal(effort, &level) != nil || strings.TrimSpace(level) == "") {
		level = ""
		if len(effort) == 0 {
			level = inherited.Level
		}
		if modelVersion(model) != modelVersion(inherited.Model) {
			color = cMuted
		}
	}
	if level != "" {
		if family == "" {
			return color + modelglyph.EffortLabel(level) + reset
		}
		return cModel + family + reset + cMuted + "·" + reset + color + modelglyph.EffortLabel(level) + reset
	}
	if family == "" {
		return ""
	}
	return cModel + family + reset
}

// modelVersion strips the vendor prefix and the context-window tag:
// claude-opus-5-5[1m] → opus-5-5.
func modelVersion(model string) string {
	model = strings.TrimPrefix(strings.TrimSpace(model), "claude-")
	if cut := strings.IndexByte(model, '['); cut >= 0 {
		model = model[:cut]
	}
	return model
}

// modelFamily is the model's family alone: claude-opus-5-5[1m] → opus.
func modelFamily(model string) string {
	model = modelVersion(model)
	if cut := strings.IndexByte(model, '-'); cut > 0 {
		model = model[:cut]
	}
	return model
}

// subagentStatus renders the task status and the time since its start: live
// while it runs, frozen at its transcript's last entry once it stops. A task
// Claude Code calls stopped while an agent below it still works is
// delegating: its own turn is over, its work is not, and its clock runs on.
func subagentStatus(task *subagentTask, activity *agentActivity, now time.Time) string {
	status := strings.TrimSpace(task.Status)
	live := status == taskRunning
	if !live && activity != nil && activity.nest.active > 0 {
		status, live = taskDelegating, true
	}
	color := ""
	switch status {
	case taskDelegating:
		color = cDelegating
	case taskRunning:
		color = cRunning
	case taskCompleted:
		color = cCompleted
	case "failed", "killed", "error":
		color = cFailed
	}
	segment := ""
	if status != "" {
		segment = color + status + reset
	}
	if task.StartTime <= 0 {
		return segment
	}
	end := now
	if !live && activity != nil && !activity.last.IsZero() {
		end = activity.last
	}
	ran := time.Duration(max(end.UnixMilli()-task.StartTime, 0)) * time.Millisecond
	elapsed := cElapsed + formatCacheTime(ran, false) + reset
	if segment == "" {
		return elapsed
	}
	return segment + " " + elapsed
}

// activitySegments renders idle → tools → errors → cache → compactions from
// the transcript; idle, errors and compactions appear only when they say
// something. The cache segment has the main line's shape: the time left on the
// agent's own prompt cache, then the share of its newest call's prompt read
// from it. A transcript that could not be read shows "tools ?" and "💾!".
func activitySegments(activity agentActivity, running bool, now time.Time) []rowPart {
	if activity.err != nil {
		return []rowPart{{text: cBad + "💾!" + reset}}
	}
	var parts []rowPart
	if quiet := now.Sub(activity.last); running && !activity.last.IsZero() && quiet >= stallAfter {
		color := cWarn
		if quiet >= 5*stallAfter {
			color = cBad
		}
		parts = append(parts, rowPart{text: color + "idle " + formatDuration(quiet.Milliseconds()) + reset})
	}
	if activity.errors > 0 {
		parts = append(parts, rowPart{text: cBad + plural(activity.errors, "error") + reset})
	}
	parts = append(parts, rowPart{text: agentCacheText(activity.transcript, activity.cacheHit, now), drop: dropCache})
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
// billed. A torn final line — the agent is mid-write — is skipped, not an error.
func readAgentActivity(sessionTranscript, id string, prices *pricing.Prices) agentActivity {
	activity := agentActivity{cacheHit: -1}
	if strings.TrimSpace(sessionTranscript) == "" {
		activity.err = errors.New("payload names no session transcript")
		activity.roleErr = activity.err
		return activity
	}
	base := filepath.Join(strings.TrimSuffix(sessionTranscript, ".jsonl"), "subagents", "agent-"+id)
	activity.role, activity.roleErr = readAgentRole(base + ".meta.json")
	path := base + ".jsonl"
	activity.transcript = path
	file, err := os.Open(path)
	if err != nil {
		activity.err = fmt.Errorf("open sub-agent transcript %s: %w", path, err)
		return activity
	}
	defer func() { _ = file.Close() }() // read-only handle; a close error loses nothing

	seen := map[string]struct{}{}
	spend := newSpendScan(prices)
	reader := bufio.NewReaderSize(file, 1<<16)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			recordTranscriptLine(line, seen, &activity, spend)
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				activity.err = fmt.Errorf("read sub-agent transcript %s: %w", path, readErr)
			}
			break
		}
	}
	activity.tools = len(seen)
	activity.spend = spend.total()
	activity.spend.tools = activity.tools
	return activity
}

// readAgentRole reads agentType from the sub-agent's meta file.
func readAgentRole(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read sub-agent meta %s: %w", path, err)
	}
	var meta struct {
		AgentType string `json:"agentType"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return "", fmt.Errorf("parse sub-agent meta %s: %w", path, err)
	}
	if strings.TrimSpace(meta.AgentType) == "" {
		return "", fmt.Errorf("sub-agent meta %s names no agentType", path)
	}
	return meta.AgentType, nil
}

func recordTranscriptLine(line []byte, seen map[string]struct{}, activity *agentActivity, spend *spendScan) {
	var entry struct {
		Type      string    `json:"type"`
		Subtype   string    `json:"subtype"`
		Timestamp time.Time `json:"timestamp"`
		Effort    any       `json:"effort"`
		Message   struct {
			ID      string          `json:"id"`
			Model   string          `json:"model"`
			Content json.RawMessage `json:"content"`
			Usage   *billedUsage    `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return
	}
	if entry.Timestamp.After(activity.last) {
		activity.last = entry.Timestamp
	}
	if entry.Type == "system" && entry.Subtype == "compact_boundary" {
		activity.compactions++
		return
	}
	if entry.Type != entryAssistant && entry.Type != entryUser {
		return
	}
	var blocks []struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		IsError bool   `json:"is_error"`
	}
	if json.Unmarshal(entry.Message.Content, &blocks) == nil {
		for _, block := range blocks {
			switch {
			case entry.Type == entryAssistant && block.Type == blockToolUse && block.ID != "":
				seen[block.ID] = struct{}{}
			case entry.Type == entryUser && block.Type == "tool_result" && block.IsError:
				activity.errors++
			}
		}
	}
	if entry.Type != entryAssistant {
		return
	}
	// The level the request went out at; a model that takes none leaves it out.
	if level, ok := entry.Effort.(string); ok && strings.TrimSpace(level) != "" {
		activity.effort = strings.TrimSpace(level)
	}
	spend.record(entry.Message.ID, entry.Message.Model, entry.Message.Usage)
	if usage := entry.Message.Usage; usage != nil {
		if hit := cacheHitPercent(usage.CacheRead, usage.CacheCreation, usage.Input); hit >= 0 {
			activity.cacheHit = hit
		}
	}
}
