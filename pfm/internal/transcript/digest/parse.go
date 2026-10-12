package digest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/jsvalue"
	"github.com/rezzminator/professor/pfm/internal/pyvalue"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// detectWindow is how many records engine detection reads.
const detectWindow = 80

// The engine names a digest prints, the engines' CLI spellings.
var (
	engineClaude   = pfmengine.MustLookup(pfmengine.Claude).LongName
	engineCodex    = pfmengine.MustLookup(pfmengine.Codex).LongName
	engineOpenCode = pfmengine.MustLookup(pfmengine.OpenCode).LongName
)

// Event kinds and dispositions.
const (
	kindPrompt = "prompt"
	kindReply  = "reply"
	kindCall   = "call"
	kindNote   = "note"
	kindFinal  = "final"

	dispRendered = "rendered"
	dispHeader   = "header"
	dispSkipped  = "skipped"

	compactedNote = "COMPACTED — the context was compacted here"

	codexResponseItem = "response_item"
	codexEventMsg     = "event_msg"
	noneWord          = "none"
	claudeSystem      = "system"
)

var (
	notificationPrefixes = []string{"[SYSTEM NOTIFICATION", "<task-notification>", "<system-reminder>"}
	pluginPrompt         = regexp.MustCompile(`^The \S+ plugin sent a message:`)
	codexTypes           = []string{"session_meta", codexResponseItem, codexEventMsg, "turn_context", "world_state"}
	claudeTypes          = []string{
		transcript.RoleUser,
		transcript.RoleAssistant,
		"attachment",
		claudeSystem,
		"summary",
	}
)

// event is one rendered thing: a prompt, a reply, a call with its result, a
// note or the final reply, at its transcript line.
type event struct {
	line                 int
	ts, kind, name, text string
	sub, raw             string
	result               *string
	err, cmd, hidden     bool
	dur                  *float64
	children             int
	matches              []string
}

// full is every text the event carries, for --grep.
func (ev *event) full() string {
	var parts []string
	for _, part := range []string{ev.text, ev.raw} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if ev.result != nil && *ev.result != "" {
		parts = append(parts, *ev.result)
	}
	return strings.Join(parts, "\n")
}

// setResult records a call's result; its duration, once, from the call's and
// the result's stamps.
func (ev *event) setResult(text string, failed bool, ts string) {
	ev.result, ev.err = &text, failed
	start, startOK := parseStamp(ev.ts)
	end, endOK := parseStamp(ts)
	if startOK && endOK && ev.dur == nil {
		seconds := end.Sub(start).Seconds()
		ev.dur = &seconds
	}
}

// disposition is what became of one record.
type disposition struct{ state, label string }

// parsed is one transcript read into events, every record's disposition kept
// in the order its line was first marked; an OpenCode session's records are
// its message and part rows.
type parsed struct {
	path, engine    string
	bytes           int64
	records         int
	messages, parts int
	events          []*event
	order           []int
	disp            map[int]disposition
	meta            map[string]string
	nulls           map[string]bool
	title           string
	t0, t1          *time.Time
	pending         map[string]*event
	calls           map[string]*event
	wrappers        []*event
}

func (p *parsed) mark(line int, state, label string) {
	if _, seen := p.disp[line]; !seen {
		p.order = append(p.order, line)
	}
	p.disp[line] = disposition{state: state, label: label}
}

func (p *parsed) add(ev *event, label string) {
	p.events = append(p.events, ev)
	p.mark(ev.line, dispRendered, label)
}

// setMeta sets key unless it is set.
func (p *parsed) setMeta(key, value string) {
	if _, set := p.meta[key]; !set {
		p.meta[key] = value
	}
}

// setMetaValue sets key, unless set, to a member's str(), "" when absent; a
// null is Python's None: printed "None", absent to every truthiness test
// (truthyMeta).
func (p *parsed) setMetaValue(key string, raw json.RawMessage) {
	if _, set := p.meta[key]; set {
		return
	}
	p.meta[key] = stringOr(raw, "")
	if raw != nil && pyvalue.IsNull(raw) {
		p.nulls[key] = true
	}
}

// truthyMeta is key's value where Python tests it for truth: "" when it was
// a null.
func (p *parsed) truthyMeta(key string) string {
	if p.nulls[key] {
		return ""
	}
	return p.meta[key]
}

func (p *parsed) stamp(ts string) {
	at, ok := parseStamp(ts)
	if !ok {
		return
	}
	if p.t0 == nil || at.Before(*p.t0) {
		p.t0 = &at
	}
	if p.t1 == nil || at.After(*p.t1) {
		p.t1 = &at
	}
}

// parseStamp reads a record's timestamp, a stamp without an offset in UTC.
func parseStamp(ts string) (time.Time, bool) {
	if ts == "" {
		return time.Time{}, false
	}
	at, ok := transcript.ParseTimestamp(ts, time.UTC)
	return at.UTC(), ok
}

// bufferedRecord is a record held until the engine is known.
type bufferedRecord struct {
	line int
	raw  []byte
}

// contextReader is a reader that stops once its context is done, so a long
// parse honours a deadline between reads.
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

// parseTranscript streams path into events. A line holding no JSON object is
// an unparseable record; an empty transcript, one with no object at all, or
// one whose first records name no engine is a failure. A done ctx stops the
// read and is returned as it is.
func parseTranscript(ctx context.Context, path string) (*parsed, error) {
	p := &parsed{
		path: path, disp: map[int]disposition{}, meta: map[string]string{}, nulls: map[string]bool{},
		pending: map[string]*event{}, calls: map[string]*event{},
	}
	var (
		buffer  []bufferedRecord
		number  int
		objects int
		unacc   []int
	)
	handle := func(line int, raw []byte) {
		switch {
		case raw == nil:
			p.mark(line, dispSkipped, "unparseable line")
		case p.engine == engineCodex:
			p.codexRecord(line, raw)
		default:
			p.claudeRecord(line, raw)
		}
		if _, marked := p.disp[line]; !marked {
			unacc = append(unacc, line)
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, runtimeFailure("cannot read %s: %v", path, err)
	}
	defer func() { _ = file.Close() }()
	_, _, err = transcript.ScanLines(contextReader{ctx: ctx, reader: file}, 0, func(line []byte, _ bool) {
		number++
		trimmed := bytes.Trim(line, " \t\n\r\v\f")
		if len(trimmed) == 0 {
			return
		}
		p.records++
		var raw []byte
		if utf8.Valid(trimmed) && trimmed[0] == '{' && json.Valid(trimmed) {
			raw = trimmed
			objects++
		}
		if p.engine != "" {
			handle(number, raw)
			return
		}
		if len(buffer) < detectWindow {
			if raw != nil {
				raw = bytes.Clone(raw)
			}
			buffer = append(buffer, bufferedRecord{line: number, raw: raw})
			if engine := detectEngine(raw); engine != "" {
				p.engine = engine
				for _, held := range buffer {
					handle(held.line, held.raw)
				}
				buffer = nil
			}
		}
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, runtimeFailure("cannot read %s: %v", path, err)
	}
	if p.records == 0 {
		return nil, runtimeFailure("empty transcript: %s", path)
	}
	if objects == 0 {
		return nil, runtimeFailure("no line of %s parsed as a JSON object (%d lines)", path, p.records)
	}
	if p.engine == "" {
		return nil, runtimeFailure("no record names its engine: neither a Claude nor a Codex transcript")
	}
	for _, line := range unacc {
		p.mark(line, dispSkipped, "unaccounted")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, runtimeFailure("cannot read %s: %v", path, err)
	}
	p.bytes = info.Size()
	if p.engine == engineClaude {
		p.readMetaFile()
	}
	return p, nil
}

// detectEngine names the engine one record shows, "" when it shows none.
func detectEngine(raw []byte) string {
	if raw == nil {
		return ""
	}
	codex, _ := transcript.DecodeCodexRecord(raw)
	if kind, ok := pyvalue.String(codex.Type); (ok && slices.Contains(codexTypes, kind)) || codex.Payload != nil {
		return engineCodex
	}
	claude, _ := transcript.DecodeClaudeRecord(raw)
	if kind, ok := pyvalue.String(claude.Type); (ok && slices.Contains(claudeTypes, kind)) || claude.SessionID != nil {
		return engineClaude
	}
	return ""
}

// readMetaFile reads a sub-agent transcript's meta beside it into the header.
func (p *parsed) readMetaFile() {
	meta := transcript.SubagentMetaPath(p.path)
	if meta == p.path {
		return
	}
	if info, err := os.Stat(meta); err != nil || !info.Mode().IsRegular() {
		return
	}
	data, err := transcript.ReadSubagentMeta(meta)
	if err != nil {
		p.meta["meta_error"] = err.Error()
		return
	}
	p.meta["agent_type"], p.meta["description"] = data.AgentType, data.Description
}

// typeLabel is a record's type as a label: "?" when it is falsy.
func typeLabel(raw json.RawMessage) string {
	if !pyvalue.Bool(raw) {
		return "?"
	}
	return pyvalue.Str(raw)
}

// stringOr is raw's str() when it is a string-typed or any present value, def
// when absent.
func stringOr(raw json.RawMessage, def string) string {
	if raw == nil {
		return def
	}
	return pyvalue.Str(raw)
}

// stampOf is a timestamp member when it is a string.
func stampOf(raw json.RawMessage) string {
	text, _ := pyvalue.String(raw)
	return text
}

// member is an object's member by exact name; nil when raw is no object or
// lacks it.
func member(raw json.RawMessage, key string) json.RawMessage {
	members, ok := jsvalue.InsertionMembers(raw)
	if !ok {
		return nil
	}
	for _, field := range members {
		if field.Key == key {
			return field.Value
		}
	}
	return nil
}

// isObject reports raw as a JSON object.
func isObject(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) != 0 && raw[0] == '{'
}

// contentBlocks is a message content as the reader walks it: a string as one
// text block, a list element by element, an object as one non-block per
// member, anything else as nothing.
func contentBlocks(content json.RawMessage) []transcript.ContentBlock {
	if text, ok := pyvalue.String(content); ok {
		quoted, _ := json.Marshal(text)
		raw := json.RawMessage(`{"type":"text","text":` + string(quoted) + `}`)
		blocks, _ := transcript.DecodeContent(json.RawMessage("[" + string(raw) + "]"))
		return blocks
	}
	if blocks, ok := transcript.DecodeContent(content); ok {
		return blocks
	}
	if members, ok := jsvalue.InsertionMembers(content); ok && len(members) != 0 {
		return make([]transcript.ContentBlock, len(members))
	}
	return nil
}

// blockType is a block's type as a label: "?" for a non-object, "None" for
// an absent type.
func blockType(block *transcript.ContentBlock) string {
	if !block.IsObject() {
		return "?"
	}
	return stringOr(block.Type, "None")
}

// blockText is a block's text when it is a string.
func blockText(block *transcript.ContentBlock) (string, bool) {
	if !block.IsObject() {
		return "", false
	}
	return pyvalue.String(block.Text)
}

// hasPrefixAny reports text starting with any prefix.
func hasPrefixAny(text string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// claudeRecord parses one Claude record.
func (p *parsed) claudeRecord(n int, raw []byte) {
	record, _ := transcript.DecodeClaudeRecord(raw)
	kind, ts := typeLabel(record.Type), stampOf(record.Timestamp)
	p.stamp(ts)
	for _, pair := range []struct {
		key   string
		value json.RawMessage
	}{{"cwd", record.CWD}, {"session", record.SessionID}, {"agent", record.AgentID}} {
		if pyvalue.Bool(pair.value) {
			p.setMeta(pair.key, pyvalue.Str(pair.value))
		}
	}
	message := record.DecodeMessage()
	if message == nil {
		message = &transcript.ClaudeMessage{}
	}
	switch {
	case kind == transcript.RoleAssistant:
		p.claudeAssistant(n, ts, message)
	case kind == transcript.RoleUser:
		p.claudeUser(n, ts, &record, message)
	case kind == "attachment":
		p.claudeAttachment(n, ts, record.Attachment)
	case kind == "custom-title" || kind == "agent-name":
		if title, ok := record.Title(); ok {
			p.title = title
		}
		p.mark(n, dispHeader, kind)
	case kind == claudeSystem && stampOf(record.Subtype) == "compact_boundary":
		p.add(&event{line: n, ts: ts, kind: kindNote, text: compactedNote}, "system.compact_boundary")
	case kind == claudeSystem:
		p.mark(n, dispSkipped, "system."+stringOr(record.Subtype, "?"))
	default:
		p.mark(n, dispSkipped, kind)
	}
}

// isPyString reports raw as a JSON string.
func isPyString(raw json.RawMessage) bool {
	_, ok := pyvalue.String(raw)
	return ok
}

func (p *parsed) claudeAssistant(n int, ts string, message *transcript.ClaudeMessage) {
	if model := message.Model; pyvalue.Bool(model) && stampOf(model) != "<synthetic>" {
		p.setMeta("model", pyvalue.Str(model))
	}
	blocks := contentBlocks(message.Content)
	lastText := -1
	for index := range blocks {
		if text, ok := blockText(
			&blocks[index],
		); ok && blocks[index].Kind() == transcript.TextBlock &&
			pyvalue.Strip(text) != "" {
			lastText = index
		}
	}
	stop := stampOf(message.StopReason)
	shown := false
	var other []string
	for index := range blocks {
		block := &blocks[index]
		bt := blockType(block)
		text, isText := blockText(block)
		switch {
		case block.IsObject() && block.Kind() == transcript.TextBlock && isText && pyvalue.Strip(text) != "":
			kind := kindReply
			if (stop == "end_turn" || stop == "stop_sequence") && isPyString(message.StopReason) && index == lastText {
				kind = kindFinal
			}
			p.events = append(p.events, &event{line: n, ts: ts, kind: kind, text: pyvalue.Strip(text)})
			shown = true
		case block.IsObject() && block.Kind() == "tool_use":
			name := stringOr(block.Name, "?")
			ev := &event{line: n, ts: ts, kind: kindCall, name: name, text: claudeTarget(name, block.Input)}
			ev.raw, ev.cmd = pyvalue.Dumps(block.Input), name == "Bash"
			p.pending[pyvalue.Repr(block.ID)] = ev
			p.events = append(p.events, ev)
			shown = true
		default:
			other = append(other, "assistant."+bt)
		}
	}
	switch {
	case shown:
		p.mark(n, dispRendered, transcript.RoleAssistant)
	case len(other) != 0:
		p.mark(n, dispSkipped, other[0])
	default:
		p.mark(n, dispSkipped, "assistant.empty")
	}
}

func (p *parsed) claudeUser(n int, ts string, record *transcript.ClaudeRecord, message *transcript.ClaudeMessage) {
	if pyvalue.Bool(record.IsCompactSummary) {
		p.add(
			&event{line: n, ts: ts, kind: kindNote, text: "COMPACT SUMMARY " + transcript.FlatContent(message.Content)},
			"user.compact_summary",
		)
		return
	}
	shown, label := false, "user.empty"
	blocks := contentBlocks(message.Content)
	for index := range blocks {
		block := &blocks[index]
		bt := blockType(block)
		text, isText := blockText(block)
		switch {
		case block.IsObject() && block.Kind() == "tool_result":
			key := pyvalue.Repr(block.ToolUseID)
			ev := p.pending[key]
			if ev == nil {
				label = "user.tool_result (no matching call)"
				continue
			}
			delete(p.pending, key)
			ev.setResult(transcript.FlatContent(block.Content), pyvalue.Bool(block.IsError), ts)
			shown = true
		case block.IsObject() && block.Kind() == transcript.TextBlock && isText && pyvalue.Strip(text) != "":
			text = pyvalue.Strip(text)
			kind := kindPrompt
			if pyvalue.Bool(record.IsMeta) || hasPrefixAny(text, notificationPrefixes) {
				kind = kindNote
			}
			ev := &event{line: n, ts: ts, kind: kind, text: text}
			if kind == kindPrompt && pluginPrompt.MatchString(text) {
				ev.sub = "plugin"
			}
			p.events = append(p.events, ev)
			shown = true
		case block.IsObject() && block.Kind() == "image":
			p.events = append(p.events, &event{line: n, ts: ts, kind: kindPrompt, text: "[image]"})
			shown = true
		default:
			label = "user." + bt
		}
	}
	if shown {
		p.mark(n, dispRendered, transcript.RoleUser)
		return
	}
	p.mark(n, dispSkipped, label)
}

func (p *parsed) claudeAttachment(n int, ts string, attachment json.RawMessage) {
	if !isObject(attachment) {
		attachment = nil
	}
	if stampOf(member(attachment, "type")) != "queued_command" {
		p.mark(n, dispSkipped, "attachment."+stringOr(member(attachment, "type"), "?"))
		return
	}
	text := transcript.FlatContent(member(attachment, "prompt"))
	if hasPrefixAny(pyvalue.Strip(text), notificationPrefixes) {
		p.add(&event{line: n, ts: ts, kind: kindNote, text: pyvalue.Strip(text)},
			"attachment.queued_command (notification)")
		return
	}
	p.add(&event{line: n, ts: ts, kind: kindPrompt, text: text, sub: "queued"}, "attachment.queued_command")
}

// claudeTarget is what a Claude tool call acts on, as one line names it.
func claudeTarget(name string, input json.RawMessage) string {
	if !isObject(input) {
		return stringOr(input, "None")
	}
	get := func(key string) json.RawMessage { return member(input, key) }
	switch name {
	case "Bash":
		return stringOr(get("command"), "")
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		path := ""
		if value := firstTruthy(get("file_path"), get("notebook_path")); value != nil {
			path = pyvalue.Str(value)
		}
		if name == "Read" && (pyvalue.Bool(get("offset")) || pyvalue.Bool(get("limit"))) {
			return path + " @" + stringOr(get("offset"), "1") + "+" + stringOr(get("limit"), "")
		}
		if name == "Edit" {
			old, updated := stringOr(get("old_string"), ""), stringOr(get("new_string"), "")
			return path + " (-" + strconv.Itoa(strings.Count(old, "\n")+1) + " +" +
				strconv.Itoa(strings.Count(updated, "\n")+1) + " lines)"
		}
		return path
	case "Grep", "Glob":
		var parts []string
		for _, key := range []string{"pattern", "path", "glob"} {
			if pyvalue.Bool(get(key)) {
				parts = append(parts, pyvalue.Str(get(key)))
			}
		}
		return strings.Join(parts, " ")
	case "Agent", "Task":
		return stringOr(get("subagent_type"), "") + ": " + stringOr(get("description"), "")
	}
	for _, key := range []string{"skill", "url", "query"} {
		if pyvalue.Bool(get(key)) {
			target := pyvalue.Str(get(key))
			if key == "skill" && pyvalue.Bool(get("args")) {
				target += " " + pyvalue.Str(get("args"))
			}
			return target
		}
	}
	return pyvalue.Dumps(input)
}

// firstTruthy is the first truthy value, nil when none is.
func firstTruthy(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if pyvalue.Bool(value) {
			return value
		}
	}
	return nil
}

// tally counts labels in the order first seen, Python's Counter.
type tally struct {
	keys   []string
	counts map[string]int
}

func newTally() *tally { return &tally{counts: map[string]int{}} }

func (counter *tally) add(key string) {
	if _, seen := counter.counts[key]; !seen {
		counter.keys = append(counter.keys, key)
	}
	counter.counts[key]++
}

func (counter *tally) total() int {
	sum := 0
	for _, key := range counter.keys {
		sum += counter.counts[key]
	}
	return sum
}

// mostCommon is the tally's keys, the largest count first, ties in the order
// first seen.
func (counter *tally) mostCommon() []string {
	keys := append([]string(nil), counter.keys...)
	sort.SliceStable(
		keys,
		func(left, right int) bool { return counter.counts[keys[left]] > counter.counts[keys[right]] },
	)
	return keys
}
