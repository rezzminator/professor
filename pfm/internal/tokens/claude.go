package tokens

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/jsvalue"
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// categories: what a carried token is made of, in the report's fixed order.
var categories = [...]string{
	"fixed prefix (system prompt, tool schemas, CLAUDE.md)",
	"prompts + briefs",
	"task notifications (async agent reports)",
	"own earlier output (text, tool inputs)",
	"Read · first time",
	"Read · same file again",
	"Bash · test/build/docker runs",
	"Bash · reading (cat/grep/find…)",
	"Bash · git",
	"Bash · other",
	"Grep/Glob",
	"Edit/Write acks",
	"sub-agent reports (sync)",
	"skill bodies",
	"MCP + web",
	"other tools",
	"harness injections (reminders, memory, listings)",
	"post-compaction summary",
	"unexplained delta",
	"OUTPUT generated (text + thinking + tool inputs)",
}

const (
	catPrefix = iota
	catPrompt
	catNotification
	catOwn
	catReadFirst
	catReadAgain
	catBashTest
	catBashRead
	catBashGit
	catBashOther
	catGrep
	catEdit
	catAgent
	catSkill
	catMCP
	catOther
	catHarness
	catSummary
	catUnexplained
	catOutput
	categoryCount
)

// noBash marks an event that is no Bash result.
const noBash = -1

// A test/build RUN, not merely a command that names a build tool: `dev.sh
// status` reports state and runs nothing, and a bare `tsc` inside prose is no
// invocation. A wait is a poll even when the command never repeats: one
// `sleep 300` costs a whole re-read of the context for nothing said.
var (
	bashTest = jsRegexp(
		`\b(go\s+(test|build|vet)|pytest|vitest|jest|playwright|cypress|(npm|pnpm|yarn|bun)\s+(run\s+)?(test|e2e|check|lint|typecheck|build)\S*|cargo\s+(test|build|check)|make\s+\S*(test|check|build|verify)\S*|dev\.sh\s+(all|build|test|typecheck|verify|install)\b|golangci-lint|eslint|mvn\s|gradlew?\s|phpunit|rspec|docker\s+(compose\s+)?(build|run|exec|up)|npx\s+(vitest|jest|playwright|tsc)|TEST_[A-Z_]+=|run-?lanes?|test\S*\.sh)`,
	)
	bashPoll = jsRegexp(`(^|[;&|(]\s*)(sleep|wait)\s|tail\s+-f\b|--watch\b`)
	bashGit  = jsRegexp(`(^|[;&|(]\s*|\s)(git|gh)\s`)
	bashRead = jsRegexp(`(^|[;&|(]\s*)(cat|sed|head|tail|rg|grep|find|ls|tree|wc|awk|jq|less|bat|fd|stat|diff)\b`)
	testFail = jsRegexp(`\b(FAIL|FAILED|failed|Error:|✗|×)\b|exit code [1-9]`)
	cdPrefix = jsRegexp(`^cd \S+ (&&|;) `)
	contract = jsRegexp(`(?:CLAUDE|AGENTS)\.md`)
)

func bashCategory(command string) int {
	switch {
	case bashTest.MatchString(command):
		return catBashTest
	case bashGit.MatchString(command):
		return catBashGit
	case bashRead.MatchString(command):
		return catBashRead
	}
	return catBashOther
}

// bands: the context size a call was billed at, in thousands.
var bands = [...]struct {
	below float64
	name  string
}{{50, "under 50K"}, {100, "50–100K"}, {150, "100–150K"}, {200, "150–200K"}, {400, "200–400K"}, {math.Inf(1), "over 400K"}}

// timelineBig is the tool-result size, in characters, --timeline counts as big.
const timelineBig = 20 * 1024

// claudeMessage is the record's message with its usage decoded: nil, nil
// when the record carries no message object or its usage is malformed — the
// ported reader skips such a message whole, content included.
func claudeMessage(line *transcript.ClaudeRecord) (*transcript.ClaudeMessage, *pricing.UsageBlock) {
	message := line.DecodeMessage()
	if message == nil {
		return nil, nil
	}
	usage, err := message.UsageBlock()
	if err != nil {
		return nil, nil
	}
	return message, usage
}

// callKey is the record's response key: one API response is one call however
// many content-block lines repeat its usage.
func callKey(line *transcript.ClaudeRecord, message *transcript.ClaudeMessage, file string, number int) string {
	return transcript.ResponseKey(jsString(message.ID), jsString(line.RequestID), jsString(line.UUID),
		file+":"+strconv.Itoa(number))
}

// jsString is raw's string value, "" for any other form.
func jsString(raw json.RawMessage) string {
	var text string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &text) != nil {
		return ""
	}
	return text
}

// isString reports raw as a JSON string.
func isString(raw json.RawMessage) bool { return len(raw) > 0 && raw[0] == '"' }

// truthy is JavaScript truthiness of a decoded value; an absent one is false.
func truthy(raw json.RawMessage) bool {
	switch text := string(raw); {
	case text == "", text == jsNull, text == "false", text == `""`:
		return false
	case raw[0] == '-' || (raw[0] >= '0' && raw[0] <= '9'):
		value, err := strconv.ParseFloat(text, 64)
		return err != nil || (value != 0 && !math.IsNaN(value))
	}
	return true
}

// keyString is String(value) for a value used as an object key or printed: a
// string as itself, a number in JavaScript form, an object as "[object Object]".
func keyString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return jsUndefined
	}
	switch raw[0] {
	case '"':
		return jsString(raw)
	case '{':
		return "[object Object]"
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return ""
		}
		parts := make([]string, len(items))
		for i, item := range items {
			if string(item) != jsNull {
				parts[i] = keyString(item)
			}
		}
		return strings.Join(parts, ",")
	case 't', 'f', 'n':
		return string(raw)
	}
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		return string(raw)
	}
	return num(value)
}

// coalesce is a ?? b ?? …: the first value neither absent nor null.
func coalesce(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if len(value) != 0 && string(value) != jsNull {
			return value
		}
	}
	return nil
}

// firstOf is a || b || …: the first truthy string, else "".
func firstOf(values ...json.RawMessage) string {
	for _, value := range values {
		if truthy(value) {
			return keyString(value)
		}
	}
	return ""
}

// stringifiedLength is JSON.stringify(value).length.
func stringifiedLength(raw json.RawMessage) float64 {
	text, err := jsvalue.StringifyRaw(raw)
	if err != nil {
		return 0
	}
	return float64(utf16Len(text))
}

// resultChars is the characters a tool result or prompt carries: text by its
// length, an image as 6000, anything else by its JSON.
func resultChars(raw json.RawMessage) float64 {
	if isString(raw) {
		return float64(utf16Len(jsString(raw)))
	}
	if total, ok := blockChars(raw); ok {
		return total
	}
	if truthy(raw) {
		return stringifiedLength(raw)
	}
	return 0
}

// blockChars is resultChars of a content-block list, each item decoded once;
// ok is false when raw is no list.
func blockChars(raw json.RawMessage) (float64, bool) {
	list, isList := transcript.DecodeContent(raw)
	if !isList {
		return 0, false
	}
	total := 0.0
	for k := range list {
		block := &list[k]
		switch block.Kind() {
		case blockText:
			total += float64(utf16Len(jsString(block.Text)))
		case "image":
			total += 6000
		default:
			total += stringifiedLength(block.Raw)
		}
	}
	return total, true
}

// toolTarget is what a tool call aimed at, one line, whatever the tool.
func toolTarget(input json.RawMessage) string {
	var fields struct {
		FilePath, Command, Pattern, Path, SubagentType, Skill, URL, Query json.RawMessage
	}
	_ = json.Unmarshal(input, &struct {
		FilePath     *json.RawMessage `json:"file_path"`
		Command      *json.RawMessage `json:"command"`
		Pattern      *json.RawMessage `json:"pattern"`
		Path         *json.RawMessage `json:"path"`
		SubagentType *json.RawMessage `json:"subagent_type"`
		Skill        *json.RawMessage `json:"skill"`
		URL          *json.RawMessage `json:"url"`
		Query        *json.RawMessage `json:"query"`
	}{
		&fields.FilePath, &fields.Command, &fields.Pattern, &fields.Path,
		&fields.SubagentType, &fields.Skill, &fields.URL, &fields.Query,
	})
	target := ""
	if value := coalesce(fields.FilePath, fields.Command, fields.Pattern, fields.Path,
		fields.SubagentType, fields.Skill, fields.URL, fields.Query); value != nil {
		target = keyString(value)
	} else if members, ok := jsvalue.Members(input); ok {
		for _, member := range members {
			if isString(member.Value) {
				target = jsString(member.Value)
				break
			}
		}
	}
	target = jsTrim(collapseSpaces(target))
	if utf16Len(target) > 100 {
		return utf16Slice(target, 0, 99) + "…"
	}
	return target
}

// inputString is String(input[field] || ""): a tool input's string field.
func inputString(input json.RawMessage, fields ...string) string {
	var members map[string]json.RawMessage
	if json.Unmarshal(input, &members) != nil {
		return ""
	}
	for _, field := range fields {
		if value := members[field]; truthy(value) {
			return keyString(value)
		}
	}
	return ""
}

// event is one step of a transcript the replay walks: a model call, a
// compaction, or something that entered the context (a prompt, a tool
// result, a harness injection).
type event struct {
	compact bool
	call    string
	cat     int
	chars   float64
	ts      float64 // NaN without a timestamp
	trig    string
	tool    string // "" when no tool result
	target  string
	bash    int // the Bash category, noBash for any other event
	err     bool
	dur     float64
	att     string // the attachment type, "" when no injection
}

// callUsage is one call as its last record states it.
type callUsage struct {
	usage    pricing.ClaudeUsage
	split    bool
	requests []pricing.ClaudeUsage  // the executor's requests, which sum to usage; none when only the advisor billed
	advisors []pricing.AdvisorUsage // the advisor consultations the response billed beside its own usage
	unknown  []string               // the usage iteration types pfm does not know
	thinking int64
	model    string
	noModel  bool // the record named no model at all
	ts       float64
	effort   string
	copied   bool
}

// toolUse is an issued tool call, kept until its result arrives.
type toolUse struct {
	name  string
	input json.RawMessage
	ts    float64
}

// timelineSlot is one tool a --timeline call issued.
type timelineSlot struct {
	name, target string
	chars        float64
	hasResult    bool
	err          bool
	dur          float64
}

// briefStats is what a sub-agent did with its brief.
type briefStats struct {
	tests, testFails                      float64
	testCmd                               *object
	readsBeforeEdit, firstEditCall, edits float64
	editFiles                             *object
}

// collected is a transcript read into its replay events.
type collected struct {
	seq         []event
	usage       map[string]*callUsage
	order       int // distinct calls seen
	seenReads   map[string]bool
	allCmd      map[string]float64
	title       string
	aiTitle     string
	cwd         string
	harnessUSD  *float64
	brief       *string
	stats       briefStats
	issued      map[string][]*timelineSlot
	lines, bad  int
	recT0       float64
	recT1       float64
	big         float64
	isSub       bool
	sid         string
	meta        transcript.SubagentMeta
	timelineRun bool
}

// collect reads file into replay events. origins names each call's owning
// transcript when more than one holds it.
func (a *auditor) collect(file string, owners map[string]string) (*collected, error) {
	c := &collected{
		usage: map[string]*callUsage{}, seenReads: map[string]bool{}, allCmd: map[string]float64{},
		issued: map[string][]*timelineSlot{}, recT0: math.Inf(1),
		isSub: transcript.IsSubagentTranscript(file), sid: transcript.TranscriptSession(file),
		stats:       briefStats{testCmd: newObject(), editFiles: newObject()},
		timelineRun: a.timelineMode,
	}
	if c.isSub {
		meta, err := transcript.ReadSubagentMeta(transcript.SubagentMetaPath(file))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			a.scan.readErrors = append(a.scan.readErrors, err.Error())
		}
		c.meta = meta
	}
	toolUses := map[string]toolUse{}
	slots := map[string]*timelineSlot{}
	err := eachLine(file, func(raw []byte) {
		c.lines++
		line, decoded := transcript.DecodeClaudeRecord(raw)
		kind, typed := line.Kind()
		if !decoded || !typed {
			a.scan.badLines++
			c.bad++
			return
		}
		ts := math.NaN()
		if truthy(line.Timestamp) {
			ts, _ = dateParse(keyString(line.Timestamp), a.loc)
		}
		if c.timelineRun && !math.IsNaN(ts) {
			c.recT0, c.recT1 = math.Min(c.recT0, ts), math.Max(c.recT1, ts)
		}
		if c.cwd == "" && truthy(line.CWD) {
			c.cwd = keyString(line.CWD)
		}
		switch kind {
		case "custom-title", "agent-name":
			if title, named := line.Title(); named {
				c.title = title
			}
			return
		case "ai-title":
			if truthy(line.AITitle) {
				c.aiTitle = keyString(line.AITitle)
			}
			return
		case "cost-state":
			var cost float64
			if len(line.TotalCostUSD) != 0 && json.Unmarshal(line.TotalCostUSD, &cost) == nil {
				c.harnessUSD = &cost
			}
			return
		case "system":
			if jsString(line.Subtype) == "compact_boundary" {
				c.seq = append(c.seq, event{compact: true})
				return
			}
		}
		message, usage := claudeMessage(&line)
		switch {
		case kind == "assistant" && usage != nil:
			a.collectCall(c, &line, message, usage, file, ts, toolUses, slots, owners)
		case kind == "user":
			a.collectUser(c, &line, message, ts, toolUses, slots)
		case kind == "attachment" && jsString(attachmentType(line.Attachment)) != "prompt_snapshot":
			att := firstOf(attachmentType(line.Attachment))
			if att == "" {
				att = "?"
			}
			c.seq = append(
				c.seq,
				event{cat: catHarness, chars: stringifiedLength(line.Attachment), ts: ts, bash: noBash, att: att},
			)
		}
	})
	if err != nil {
		return nil, err
	}
	// a timeline over a file with no parseable line is a failure to read it, never an empty run
	if c.timelineRun && c.lines != 0 && c.bad == c.lines {
		return nil, fmt.Errorf("no line parsed as JSON (%d malformed)", c.bad)
	}
	return c, nil
}

func attachmentType(attachment json.RawMessage) json.RawMessage {
	var fields struct {
		Type json.RawMessage `json:"type"`
	}
	if len(attachment) == 0 || attachment[0] != '{' || json.Unmarshal(attachment, &fields) != nil {
		return nil
	}
	return fields.Type
}

func (a *auditor) collectCall(
	c *collected, line *transcript.ClaudeRecord, message *transcript.ClaudeMessage, block *pricing.UsageBlock,
	file string, ts float64, toolUses map[string]toolUse, slots map[string]*timelineSlot, owners map[string]string,
) {
	usage, split := block.Billed()
	advisors := block.Advisors()
	model := jsString(message.Model)
	if model == "<synthetic>" || usage == (pricing.ClaudeUsage{}) && len(advisors) == 0 {
		a.scan.syntheticCalls++
		return
	}
	if math.IsNaN(ts) {
		a.scan.noTimestamp++
		return
	}
	if !truthy(message.ID) && !truthy(line.RequestID) {
		a.scan.identityUnknownRecords++
	}
	id := callKey(line, message, file, c.lines)
	var forked struct {
		SessionID json.RawMessage `json:"sessionId"`
	}
	if len(line.ForkedFrom) != 0 && line.ForkedFrom[0] == '{' {
		_ = json.Unmarshal(line.ForkedFrom, &forked)
	}
	fork := firstOf(forked.SessionID)
	current := firstOf(line.SessionID)
	if current == "" {
		current = c.sid
	}
	owner, owned := owners[id]
	copied := (fork != "" && fork != current) || (owned && owner != file)
	if _, seen := c.usage[id]; !seen {
		c.seq = append(c.seq, event{call: id, bash: noBash})
		c.order++
	}
	effort := "-"
	if value := coalesce(line.Effort, line.PerTurnEffort); value != nil {
		effort = keyString(value)
	}
	c.usage[id] = &callUsage{
		usage: usage, split: split, requests: block.Requests(), advisors: advisors, unknown: block.UnknownIterations(),
		thinking: block.Thinking(), model: model,
		noModel: len(message.Model) == 0 || string(message.Model) == jsNull, ts: ts, effort: effort, copied: copied,
	}
	list, _ := transcript.DecodeContent(message.Content)
	for k := range list {
		use := &list[k]
		if use.Kind() != "tool_use" {
			continue
		}
		input := use.Input
		if !truthy(input) {
			input = json.RawMessage(`{}`)
		}
		blockID := keyString(use.ID)
		toolUses[blockID] = toolUse{name: keyString(use.Name), input: input, ts: ts}
		if c.timelineRun {
			slot := &timelineSlot{name: keyString(use.Name), target: toolTarget(input)}
			slots[blockID] = slot
			c.issued[id] = append(c.issued[id], slot)
		}
	}
}

func (a *auditor) collectUser(
	c *collected, line *transcript.ClaudeRecord, message *transcript.ClaudeMessage, ts float64,
	toolUses map[string]toolUse, slots map[string]*timelineSlot,
) {
	var content json.RawMessage
	if message != nil {
		content = message.Content
	}
	var origin struct {
		Kind json.RawMessage `json:"kind"`
	}
	if len(line.Origin) != 0 && line.Origin[0] == '{' {
		_ = json.Unmarshal(line.Origin, &origin)
	}
	notification := jsString(origin.Kind) == "task-notification"
	textCat, trig := catPrompt, "user prompt"
	switch {
	case notification:
		textCat, trig = catNotification, "agent report"
	case truthy(line.IsCompactSummary):
		textCat = catSummary
	case truthy(line.IsMeta):
		textCat = catHarness
	}
	list, isList := transcript.DecodeContent(content)
	if c.brief == nil && c.isSub && !notification && !truthy(line.IsMeta) {
		if isString(content) {
			brief := jsString(content)
			c.brief = &brief
		} else if isList {
			var texts []string
			for k := range list {
				block := &list[k]
				if block.Kind() == blockText {
					texts = append(texts, jsString(block.Text))
				}
			}
			if len(texts) != 0 {
				brief := strings.Join(texts, "\n")
				c.brief = &brief
			}
		}
	}
	if isString(content) {
		c.seq = append(
			c.seq,
			event{cat: textCat, chars: float64(utf16Len(jsString(content))), ts: ts, trig: trig, bash: noBash},
		)
		return
	}
	for k := range list {
		block := &list[k]
		switch block.Kind() {
		case "tool_result":
			c.seq = append(c.seq, a.toolResult(c, block, ts, toolUses))
			if c.timelineRun {
				result := c.seq[len(c.seq)-1]
				if result.chars > timelineBig {
					c.big++
				}
				if slot := slots[keyString(block.ToolUseID)]; slot != nil {
					slot.chars, slot.hasResult, slot.err, slot.dur = result.chars, true, result.err, result.dur
				}
			}
		case blockText:
			c.seq = append(
				c.seq,
				event{cat: textCat, chars: float64(utf16Len(jsString(block.Text))), ts: ts, trig: trig, bash: noBash},
			)
		case "image":
			c.seq = append(c.seq, event{cat: textCat, chars: 6000, ts: ts, trig: "user prompt", bash: noBash})
		}
	}
}

func (a *auditor) toolResult(
	c *collected, block *transcript.ContentBlock, ts float64, toolUses map[string]toolUse,
) event {
	use, known := toolUses[keyString(block.ToolUseID)]
	name := "?"
	if known && use.name != "" {
		name = use.name
	}
	result := event{
		cat: catOther, chars: resultChars(block.Content), ts: ts, trig: triggerToolResult, tool: name, bash: noBash,
		err: truthy(block.IsError),
	}
	if known && !math.IsNaN(ts) {
		result.dur = math.Max(0, ts-use.ts)
	}
	stats := &c.stats
	switch {
	case name == toolRead:
		result.target = inputString(use.input, "file_path")
		if stats.firstEditCall == 0 {
			stats.readsBeforeEdit++
		}
		result.cat = catReadFirst
		if c.seenReads[result.target] {
			result.cat = catReadAgain
		}
		c.seenReads[result.target] = true
	case name == toolBash:
		full := collapseSpaces(inputString(use.input, "command"))
		result.target = utf16Slice(full, 0, 140)
		result.bash = bashCategory(full)
		result.cat = result.bash
		if result.bash == catBashTest {
			stats.tests++
			text := jsString(block.Content)
			if !isString(block.Content) {
				raw := block.Content
				if !truthy(raw) {
					raw = json.RawMessage(`""`)
				}
				text, _ = jsvalue.StringifyRaw(raw)
			}
			if result.err || testFail.MatchString(utf16Slice(text, 0, 40000)) {
				stats.testFails++
			}
			stats.testCmd.bump(utf16Slice(cdPrefix.ReplaceAllString(full, ""), 0, 200), 1)
		}
		if result.bash == catBashRead && stats.firstEditCall == 0 {
			stats.readsBeforeEdit++
		}
		c.allCmd[result.target]++
	case name == "Edit" || name == "Write" || name == "MultiEdit" || name == "NotebookEdit":
		result.cat = catEdit
		result.target = inputString(use.input, "file_path")
		stats.edits++
		stats.editFiles.bump(result.target, 1)
		if stats.firstEditCall == 0 {
			stats.firstEditCall = float64(c.order)
		}
	case name == "Grep" || name == "Glob" || name == "LS":
		if stats.firstEditCall == 0 {
			stats.readsBeforeEdit++
		}
		result.cat = catGrep
		result.target = inputString(use.input, "pattern", "path")
	case name == "Agent" || name == "Task":
		result.cat = catAgent
		result.target = inputString(use.input, "subagent_type", "description")
	case name == "Skill":
		result.cat = catSkill
		result.target = inputString(use.input, "skill")
	case strings.HasPrefix(name, "mcp__") || strings.HasPrefix(name, "Web"):
		result.cat = catMCP
	}
	return result
}
