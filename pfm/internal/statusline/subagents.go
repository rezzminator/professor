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
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// subagentInput is the row context Claude Code's subagentStatusLine setting
// writes to stdin on every agent-panel tick: one entry per task row it shows.
type subagentInput struct {
	SessionID      string         `json:"session_id"`
	TranscriptPath string         `json:"transcript_path"`
	Cwd            string         `json:"cwd"`
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
	TokenSamples      []int64         `json:"tokenSamples"`
}

// subagentRow is one line Claude Code accepts back: the task id and the body
// it renders in that task's agent-panel row.
type subagentRow struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

// agentActivity is what a sub-agent's own files add to the payload: its role
// from agent-<id>.meta.json, the rest from agent-<id>.jsonl. cacheHit is -1
// until the first model turn lands; err means the transcript could not be
// read and roleErr the meta file, each rendering "?" — never as zero or empty.
type agentActivity struct {
	role        string
	roleErr     error
	tools       int
	errors      int
	compactions int
	cacheHit    int
	last        time.Time
	err         error
}

const (
	subagentBarWidth = 8
	localAgentTask   = "local_agent"
	taskRunning      = "running"
	entryAssistant   = "assistant"
	entryUser        = "user"
	// stallAfter is the quiet time before a running agent's row says idle:
	// shorter gaps are ordinary model latency.
	stallAfter = time.Minute
)

// sparkLevels is the TUI's scan-line ladder (internal/ui usageSpark), low to
// high: block elements ▁…█ and braille are WebGL custom glyphs in VS Code's
// terminal and render stale under repaint; cmd/pfm/webgl_glyph_guard_test.go
// pins the banned ranges.
var sparkLevels = []rune("_⎽⎼⎻⎺¯")

// ServeSubagents answers Claude Code's subagentStatusLine command: stdin's
// row context in, one {id,content} JSON line per agent-panel row out.
// Fail-open like the main line — any error prints nothing and exits 0, so
// every row keeps Claude Code's own body instead of the harness logging a
// failed command each tick.
func ServeSubagents(stdin io.Reader, stdout, stderr io.Writer, sidDir string) int {
	raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		fmt.Fprintf(stderr, "pfm statusline --subagents: read input (fail-open): %v\n", err)
		return 0
	}
	rows, err := RenderSubagents(raw, clock.Real.Now(), sidDir, stderr)
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
// that cannot be read renders "?" in its row and its cause goes to warn.
func RenderSubagents(raw []byte, now time.Time, sidDir string, warn io.Writer) (string, error) {
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
	for index := range data.Tasks {
		task := &data.Tasks[index]
		if task.ID == "" || (task.TokenCount <= 0 && task.ContextWindowSize <= 0) {
			continue
		}
		var activity *agentActivity
		if task.Type == localAgentTask {
			read := readAgentActivity(data.TranscriptPath, task.ID)
			for index, err := range []error{read.roleErr, read.err} {
				if err != nil && (index == 0 || err != read.roleErr) {
					fmt.Fprintf(warn, "pfm statusline --subagents: row %s: %v\n", task.ID, err)
				}
			}
			activity = &read
		}
		row := subagentRow{ID: task.ID, Content: subagentContent(task, activity, data.Cwd, inherited, now)}
		if err := encoder.Encode(row); err != nil {
			return "", fmt.Errorf("encode subagent row %s: %w", task.ID, err)
		}
	}
	return out.String(), nil
}

// subagentContent renders gauge → name·role → model·effort → status and time
// → idle → tools → errors → cache → compactions → growth → cwd → label; the
// label goes last because Claude Code truncates the row's tail. The cwd shows
// only when the agent works outside the session's own directory — the same
// one is noise.
func subagentContent(
	task *subagentTask,
	activity *agentActivity,
	sessionCwd string,
	inherited sessionEffortRecord,
	now time.Time,
) string {
	line := cTokens + formatContextTokens(task.TokenCount) + reset
	if task.ContextWindowSize > 0 {
		percent := int(task.TokenCount * 100 / task.ContextWindowSize)
		line = makeBar(percent, subagentBarWidth) + " " +
			percentColor(percent) + fmt.Sprintf("%d%%", percent) + reset + " " +
			line + cWindow + "/" + formatContextTokens(task.ContextWindowSize) + reset
	}
	line = appendSegment(line, subagentIdentity(task.Name, activity))
	line = appendSegment(line, subagentModel(task.Model, task.Effort, inherited))
	line = appendSegment(line, subagentStatus(task, activity, now))
	if activity != nil {
		line = appendSegment(line, activitySegments(*activity, task.Status == taskRunning, now))
	}
	line = appendSegment(line, sparkline(task.TokenSamples))
	cwd := strings.TrimSpace(task.Cwd)
	if cwd != "" && filepath.Clean(cwd) != filepath.Clean(strings.TrimSpace(sessionCwd)) {
		line = appendSegment(line, cCwd+filepath.Base(cwd)+reset)
	}
	label := strings.TrimSpace(task.Label)
	if label == "" {
		label = strings.TrimSpace(task.Description)
	}
	if label == "" {
		return rowOpen + line
	}
	return rowOpen + appendSegment(line, cLabel+label+reset)
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
// opus·● high. An effort that is not a JSON string is left out. A row without
// its own effort shows the session's recorded effort (inherited), exactly as
// a payload effort on the model it was recorded for and muted on another,
// where Claude Code may resolve a different level.
func subagentModel(model string, effort json.RawMessage, inherited sessionEffortRecord) string {
	family := modelFamily(model)
	var level string
	color := cEffort
	if len(effort) == 0 || json.Unmarshal(effort, &level) != nil || strings.TrimSpace(level) == "" {
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
			return color + effortLabel(level) + reset
		}
		return cModel + family + reset + cMuted + "·" + reset + color + effortLabel(level) + reset
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
// while it runs, frozen at its transcript's last entry once it stops.
func subagentStatus(task *subagentTask, activity *agentActivity, now time.Time) string {
	status := strings.TrimSpace(task.Status)
	color := ""
	switch status {
	case taskRunning:
		color = cRunning
	case "completed":
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
	if status != taskRunning && activity != nil && !activity.last.IsZero() {
		end = activity.last
	}
	elapsed := cElapsed + formatDuration(max(end.UnixMilli()-task.StartTime, 0)) + reset
	if segment == "" {
		return elapsed
	}
	return segment + " " + elapsed
}

// activitySegments renders idle → tools → errors → cache → compactions from
// the transcript; idle, errors and compactions appear only when they say
// something. A transcript that could not be read shows "?" for tools and cache.
func activitySegments(activity agentActivity, running bool, now time.Time) string {
	if activity.err != nil {
		return cWarn + "tools ?" + reset + sep + cWarn + "cache ?" + reset
	}
	line := ""
	if quiet := now.Sub(activity.last); running && !activity.last.IsZero() && quiet >= stallAfter {
		color := cWarn
		if quiet >= 5*stallAfter {
			color = cBad
		}
		line = color + "idle " + formatDuration(quiet.Milliseconds()) + reset
	}
	line = appendSegment(line, cTools+plural(activity.tools, "tool")+reset)
	if activity.errors > 0 {
		line = appendSegment(line, cBad+plural(activity.errors, "error")+reset)
	}
	if activity.cacheHit < 0 {
		line = appendSegment(line, cMuted+"cache –"+reset)
	} else {
		color := cBad
		switch {
		case activity.cacheHit >= 80:
			color = cGood
		case activity.cacheHit >= 50:
			color = cWarn
		}
		line = appendSegment(line, cTools+"cache "+reset+color+fmt.Sprintf("%d%%", activity.cacheHit)+reset)
	}
	if activity.compactions > 0 {
		line = appendSegment(line, cCompaction+fmt.Sprintf("⟲%d", activity.compactions)+reset)
	}
	return line
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
// boundaries, takes the cache hit from the newest assistant usage, and the
// newest entry timestamp. A torn final line — the agent is
// mid-write — is skipped, not an error.
func readAgentActivity(sessionTranscript, id string) agentActivity {
	activity := agentActivity{cacheHit: -1}
	if strings.TrimSpace(sessionTranscript) == "" {
		activity.err = errors.New("payload names no session transcript")
		activity.roleErr = activity.err
		return activity
	}
	base := filepath.Join(strings.TrimSuffix(sessionTranscript, ".jsonl"), "subagents", "agent-"+id)
	activity.role, activity.roleErr = readAgentRole(base + ".meta.json")
	path := base + ".jsonl"
	file, err := os.Open(path)
	if err != nil {
		activity.err = fmt.Errorf("open sub-agent transcript %s: %w", path, err)
		return activity
	}
	defer func() { _ = file.Close() }() // read-only handle; a close error loses nothing

	seen := map[string]struct{}{}
	reader := bufio.NewReaderSize(file, 1<<16)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			recordTranscriptLine(line, seen, &activity)
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				activity.err = fmt.Errorf("read sub-agent transcript %s: %w", path, readErr)
			}
			break
		}
	}
	activity.tools = len(seen)
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

func recordTranscriptLine(line []byte, seen map[string]struct{}, activity *agentActivity) {
	var entry struct {
		Type      string    `json:"type"`
		Subtype   string    `json:"subtype"`
		Timestamp time.Time `json:"timestamp"`
		Message   struct {
			Content json.RawMessage `json:"content"`
			Usage   *struct {
				Input         int64 `json:"input_tokens"`
				CacheRead     int64 `json:"cache_read_input_tokens"`
				CacheCreation int64 `json:"cache_creation_input_tokens"`
			} `json:"usage"`
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
			case entry.Type == entryAssistant && block.Type == "tool_use" && block.ID != "":
				seen[block.ID] = struct{}{}
			case entry.Type == entryUser && block.Type == "tool_result" && block.IsError:
				activity.errors++
			}
		}
	}
	if entry.Type != entryAssistant {
		return
	}
	if usage := entry.Message.Usage; usage != nil {
		if total := usage.Input + usage.CacheRead + usage.CacheCreation; total > 0 {
			activity.cacheHit = int(usage.CacheRead * 100 / total)
		}
	}
}

// sparkline draws the recent token samples scaled to their own peak: the
// gauge already says how full the window is, so this line shows the shape —
// steady growth, a stall, or the drop of a compaction. Fewer than two samples
// draw nothing.
func sparkline(samples []int64) string {
	if len(samples) < 2 {
		return ""
	}
	var scale int64
	for _, sample := range samples {
		scale = max(scale, sample)
	}
	if scale <= 0 {
		return ""
	}
	var line strings.Builder
	top := int64(len(sparkLevels) - 1)
	for _, sample := range samples {
		level := min(max(sample*top/scale, 0), top)
		line.WriteRune(sparkLevels[level])
	}
	return cGrowth + line.String() + reset
}
