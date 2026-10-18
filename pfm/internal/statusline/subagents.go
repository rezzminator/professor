package statusline

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/modelglyph"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/pyvalue"
	"github.com/rezzminator/professor/pfm/internal/transcript"
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
	transcript  string
	last        time.Time
	err         error
	nest        agentNesting
	spend       agentSpend
	// turn is its last message entry.
	turn agentTurn

	noCacheWindow bool
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
	entryCostState   = "cost-state"
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

// userAgentsDir is the user's agent definitions directory, where Claude Code
// reads them: $CLAUDE_CONFIG_DIR/agents when that is set, else ~/.claude/agents.
func userAgentsDir(home string) string {
	if dir := pfmconfig.AmbientClaudeConfigDir(); dir != "" {
		return filepath.Join(dir, "agents")
	}
	return filepath.Join(home, ".claude", "agents")
}

// ansiSGR matches one colour escape: a muted row drops them all and wears one.
var ansiSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// ServeSubagents answers Claude Code's subagentStatusLine command: stdin's
// row context in, one {id,content} JSON line per agent-panel row out.
// Fail-open like the main line — it exits 0 even on a fault, so the harness
// does not log a failed command each tick — but a fault never renders as
// absence: input that cannot be read in full or rendered turns every task row
// whose id could be read into a red "pfm statusline: {cause}" row, and the
// cause goes to stderr. Only input with no readable task id prints no row (a
// row needs an id) and says so on stderr. The price table is the machine's
// served one (pricing.LoadPrices: the clone file or the embedded copy, the
// override beside pfmConfigPath merged), never fetched; one that cannot load
// leaves every row's dollars "$?", its cause on stderr.
func ServeSubagents(
	stdin io.Reader,
	stdout, stderr io.Writer,
	machine paths.Values,
	env paths.Env,
	pfmConfigPath string,
) int {
	raw, err := readSubagentInput(stdin)
	if err != nil {
		return failSubagents("input", raw, err, stdout, stderr)
	}
	prices := LoadServedPrices(machine.Home, pfmConfigPath, stderr, "pfm statusline --subagents")
	userAgents := userAgentsDir(machine.Home)
	rows, err := RenderSubagents(raw, clock.Real.Now(), machine.SIDDir, prices, machine.Home, env, userAgents, stderr)
	if err != nil {
		return failSubagents("render", raw, err, stdout, stderr)
	}
	if _, err := io.WriteString(stdout, rows); err != nil {
		fmt.Fprintf(stderr, "pfm statusline --subagents: write output (fail-open): %v\n", err)
	}
	return 0
}

// LoadServedPrices is the machine's served price table (pricing.LoadPrices:
// the clone file or the embedded copy, the override beside pfmConfigPath
// merged), never fetched. A table that cannot load is nil — every dollar then
// renders "$?" — and its cause goes to stderr under who, as does a clone file
// the embedded copy stood in for.
func LoadServedPrices(home, pfmConfigPath string, stderr io.Writer, who string) *pricing.Prices {
	table, err := pricing.LoadPrices(home, pfmConfigPath)
	if err != nil {
		fmt.Fprintf(stderr, "%s: price table (dollars show $?): %v\n", who, err)
		return nil
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

// RenderSubagents turns one subagentStatusLine payload into the JSON lines
// Claude Code reads back, one per task that reports a model or a token count;
// a task it omits keeps Claude Code's own row body. A sub-agent transcript
// that cannot be read renders "?" in its row and its cause goes to warn. A nil
// prices leaves every row's dollars unpriced. A session with a cost ledger prices each row from it:
// the ledger rows of that agent and every agent below it (ledger_spend.go); an agent it holds no
// row of yet keeps its transcripts' price. An agent row's identity is the role its definition names
// (roleLookup), found under the session cwd up to its git root, short of home, then in userAgents.
func RenderSubagents(
	raw []byte,
	now time.Time,
	sidDir string,
	prices *pricing.Prices,
	home string,
	env paths.Env,
	userAgents string,
	warn io.Writer,
) (string, error) {
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
	roles := newRoleLookup(data.Cwd, home, userAgents, warn)
	ledger := loadLedgerView(data.SessionID, home, env, warn)
	for index := range data.Tasks {
		task := &data.Tasks[index]
		if task.ID == "" || (task.TokenCount <= 0 && task.ContextWindowSize <= 0) {
			continue
		}
		var activity *agentActivity
		if task.Type == localAgentTask {
			if tree == nil {
				tree = scanAgentTree(data.TranscriptPath, prices)
				tree.judgeLiveness(now, liveTasks(data.Tasks))
			}
			read := *tree.read(task.ID)
			read.noCacheWindow = openAIModel(task.Model) || openAIModel(read.model)
			if read.roleErr == nil {
				read.role = roles.name(read.role, warn)
			}
			read.nest = tree.nesting(task.ID)
			read.spend.add(tree.nestedSpend(task.ID))
			if ledger != nil {
				if booked, ok := ledger.spend(task.ID, tree.children); ok {
					read.spend = read.spend.withLedger(booked)
				}
			}
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
	if !terminalStatus(task.Status) {
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
		percent := gaugePercent(float64(task.TokenCount) * 100 / float64(task.ContextWindowSize))
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
// opus·▰▰▰ high. The level its transcript recorded for its latest request
// (recorded) wins: it is the effort that request went out at, after any
// plugin pinned it, so it renders plainly on any model. Without one, the
// payload's effort shows; one that is not a JSON string is left out. A row
// with neither shows the session's recorded effort (inherited), exactly as
// a payload effort on the model it was recorded for and muted on another,
// where Claude Code may resolve a different level.
func subagentModel(model, recorded string, effort json.RawMessage, inherited sessionEffortRecord) string {
	family := modelFamily(model)
	level := strings.TrimSpace(recorded)
	color, painted := cEffort, true
	if level == "" &&
		(len(effort) == 0 || json.Unmarshal(effort, &level) != nil || strings.TrimSpace(level) == "") {
		level = ""
		if len(effort) == 0 {
			level = inherited.Level
		}
		if modelVersion(model) != modelVersion(inherited.Model) {
			color, painted = cMuted, false
		}
	}
	if level != "" {
		// A muted level may be another than the one shown, so its bar stays muted
		// too; otherwise the bar wears the level's own colour.
		label := color + modelglyph.EffortLabel(level)
		if painted {
			label = paintedEffort(level, color)
		}
		if family == "" {
			return label + reset
		}
		return cModel + family + reset + cMuted + "·" + reset + label + reset
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

// modelFamily is the model's family alone: claude-opus-5-5[1m] → opus. A gpt-
// id names its tier last and its version first, so it renders tier-version:
// gpt-6-sol → sol-6, gpt-5.6-terra → terra-5.6; one with no tier renders
// whole, gpt-6 → gpt-6. The gateway's priority-tier suffix is no tier and
// drops like the window tag: gpt-6-sol-fast → sol-6, gpt-5.5-fast → gpt-5.5.
func modelFamily(model string) string {
	model = modelVersion(model)
	if strings.HasPrefix(model, "gpt-") {
		model = strings.TrimSuffix(model, "-fast")
		rest := strings.TrimPrefix(model, "gpt-")
		if cut := strings.LastIndexByte(rest, '-'); cut > 0 && cut < len(rest)-1 {
			return rest[cut+1:] + "-" + rest[:cut]
		}
		return model
	}
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
// billed. A torn final line — the agent is mid-write — is skipped, not an error.
func readAgentActivity(sessionTranscript, id string, prices *pricing.Prices) agentActivity {
	activity := agentActivity{cacheHit: -1}
	if strings.TrimSpace(sessionTranscript) == "" {
		activity.err = errors.New("payload names no session transcript")
		activity.roleErr = activity.err
		return activity
	}
	path := transcript.SubagentTranscript(transcript.SubagentsDir(sessionTranscript), id)
	activity.role, activity.roleErr = readAgentRole(transcript.SubagentMetaPath(path))
	activity.transcript = path
	seen := map[string]struct{}{}
	spend := newSpendScan(prices)
	var stamped time.Time
	if _, _, err := transcript.ReadLines(path, 0, func(line []byte, _ bool) {
		recordTranscriptLine(line, seen, &activity, spend)
		if open, delegating, at, ok := messageTurnOpen(line); ok {
			delegating = open && (delegating || activity.turn.open && activity.turn.delegating)
			activity.turn = agentTurn{open: open, delegating: delegating, last: at, seen: true}
			stamped = at
		}
	}); err != nil {
		var opened bool
		if opened, activity.err = transcriptError(path, err); !opened {
			return activity
		}
	}
	if !activity.turn.seen {
		activity.turn.open = true // no reply yet: the first turn is under way
	}
	if stamped.IsZero() {
		activity.stampTurn(path)
	}
	activity.tools = len(seen)
	activity.spend = spend.total()
	activity.spend.tools = activity.tools
	return activity
}

// roleLookup maps an agentType to the role its definition file names. It lives
// for one render, so an edited definition shows on the next one.
type roleLookup struct {
	dirs  []string // the project's agent definitions, nearest first, then the user's
	names map[string]string
}

// newRoleLookup lists the definition directories the way Claude Code collects
// them: <dir>/.claude/agents for the cwd and each parent, nearest first, through
// the git root (the first directory holding .git) and never home itself, whose
// .claude is the user's config; then the user's agents directory.
func newRoleLookup(cwd, home, userAgents string, warn io.Writer) *roleLookup {
	lookup := &roleLookup{names: map[string]string{}}
	if strings.TrimSpace(cwd) != "" {
		if home != "" {
			home = filepath.Clean(home)
		}
		for dir := filepath.Clean(cwd); dir != home; {
			lookup.dirs = append(lookup.dirs, filepath.Join(dir, ".claude", "agents"))
			_, err := os.Lstat(filepath.Join(dir, ".git"))
			if err == nil {
				break
			}
			if !errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintf(warn, "pfm statusline --subagents: git root (definitions read past %s): %v\n", dir, err)
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if userAgents != "" {
		lookup.dirs = append(lookup.dirs, userAgents)
	}
	return lookup
}

// name is the `role:` of the first definition file that exists, searched the
// way Claude Code resolves an agent (the project's, then the user's, symlinks
// followed). A type with no file, no `role:` key or an unreadable file is its
// own name; an unreadable one also reaches warn.
func (lookup *roleLookup) name(agentType string, warn io.Writer) string {
	if role, ok := lookup.names[agentType]; ok {
		return role
	}
	role := agentType
	if agentType != "" && !strings.ContainsAny(agentType, `/\`) && agentType != "." && agentType != ".." {
		for _, dir := range lookup.dirs {
			body, err := os.ReadFile(filepath.Join(dir, agentType+".md"))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				fmt.Fprintf(warn, "pfm statusline --subagents: agent definition (rows show %s): %v\n", agentType, err)
			} else if named := definitionRole(string(body)); named != "" {
				role = named
			}
			break
		}
	}
	lookup.names[agentType] = role
	return role
}

// definitionRole is the `role:` value in a definition's frontmatter, the block
// between its leading "---" line and the next one; "" when there is none.
func definitionRole(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return ""
		}
		if value, ok := strings.CutPrefix(line, "role:"); ok {
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return ""
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

func recordTranscriptLine(line []byte, seen map[string]struct{}, activity *agentActivity, spend *spendScan) {
	var entry struct {
		Type      string    `json:"type"`
		Subtype   string    `json:"subtype"`
		RequestID string    `json:"requestId"`
		Timestamp time.Time `json:"timestamp"`
		Effort    any       `json:"effort"`
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
	blocks, _ := transcript.DecodeContent(entry.Message.Content)
	for index := range blocks {
		block := &blocks[index]
		id, _ := pyvalue.String(block.ID)
		switch {
		case entry.Type == entryAssistant && block.Kind() == blockToolUse && id != "":
			seen[id] = struct{}{}
		case entry.Type == entryUser && block.Kind() == "tool_result" && string(block.IsError) == "true":
			activity.errors++
		}
	}
	if entry.Type != entryAssistant {
		return
	}
	// The level the request went out at; a model that takes none leaves it out.
	if level, ok := entry.Effort.(string); ok && strings.TrimSpace(level) != "" {
		activity.effort = strings.TrimSpace(level)
	}
	if model := entry.Message.Model; model != "" && model != "<synthetic>" {
		activity.model = model
	}
	usage := entry.Message.Usage
	if usage == nil {
		spend.record(entry.Message.ID, entry.RequestID, entry.Message.Model, nil)
		return
	}
	spend.record(entry.Message.ID, entry.RequestID, entry.Message.Model, &usage.UsageBlock)
	// A line that reports no cache counters is a call still streaming (the
	// gateway's message_start carries its whole-prompt estimate in
	// input_tokens and nothing else): reading it as a call would drop the
	// newest completed call's share to 0%.
	if usage.reportsCache {
		if hit := cacheHitPercent(usage.CacheRead, usage.CacheCreation, usage.Input); hit >= 0 {
			activity.cacheHit = hit
		}
	}
}
