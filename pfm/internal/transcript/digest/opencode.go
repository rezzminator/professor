package digest

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/pyvalue"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// OpenCode keeps a session as rows of its SQLite store (opencode.db), read
// through internal/index. The digest maps them onto its transcript model: a
// RECORD is one message row or one part row, numbered in store order (the
// session's messages by creation, each followed by its parts by id), and that
// number is the L a line carries and --lines selects; a record's type is its
// row kind and data type (message.user, part.tool, ...). A store has no file
// lines and no byte size: the header's STORE line says so instead of a FILE
// line. A session is named by its locator, {store}#{session id}.

// openCodeStoreFile is the store's file name, the one a locator's store part ends in.
const openCodeStoreFile = "opencode.db"

// OpenCode's own names this digest branches on: the tool part type, the
// failed tool state and the file tools whose input it reads.
const (
	openCodeToolPart   = "tool"
	openCodeStateError = "error"
	openCodeReadTool   = "read"
	openCodeEditTool   = "edit"
)

// openCodeSessionPrefix starts every OpenCode session id (its Identifier
// prefix "ses"): an id without it can name no OpenCode session.
const openCodeSessionPrefix = "ses_"

// openCodeLocate is nil when the locator's store holds its session, else the
// failure naming what is missing: a locator is located only once it is read.
func openCodeLocate(ctx context.Context, storePath, id string) error {
	if _, err := index.ReadOpenCodeSessionRows(ctx, filepath.Dir(storePath), id); err != nil {
		return runtimeFailure("cannot read %s: %v", openCodeLocator(storePath, id), err)
	}
	return nil
}

// openCodeLocator names one session of one store.
func openCodeLocator(storePath, id string) string { return storePath + "#" + id }

// splitOpenCodeLocator reads a locator: a store path ending in opencode.db, a
// "#", a session id.
func splitOpenCodeLocator(target string) (storePath, id string, ok bool) {
	at := strings.LastIndex(target, "#")
	if at <= 0 || at == len(target)-1 || filepath.Base(target[:at]) != openCodeStoreFile {
		return "", "", false
	}
	return target[:at], target[at+1:], true
}

// openCodeHit is one session of one store.
type openCodeHit struct {
	storePath string
	session   store.OpenCodeSession
}

func (hit openCodeHit) locator() string { return openCodeLocator(hit.storePath, hit.session.ID) }

// openCodeSessions reads every OpenCode root's sessions once per run: a root
// without a store holds none; a store that cannot be read is a failure
// naming it.
func (finder *resolver) openCodeSessions() ([]openCodeHit, []error) {
	if finder.openCode.read {
		return finder.openCode.hits, finder.openCode.failed
	}
	finder.openCode.read = true
	for _, root := range finder.env.OpenCodeRoots {
		storePath := index.OpenCodeStorePath(root)
		sessions, err := index.ReadOpenCodeSessions(finder.ctx, root)
		if err != nil {
			finder.openCode.failed = append(finder.openCode.failed, fmt.Errorf("%s (%w)", storePath, err))
			continue
		}
		if sessions != nil {
			finder.openCode.stores++
		}
		for at := range sessions {
			finder.openCode.hits = append(
				finder.openCode.hits,
				openCodeHit{storePath: storePath, session: sessions[at]},
			)
		}
	}
	return finder.openCode.hits, finder.openCode.failed
}

// msStamp is a store's millisecond time as the stamp a record carries, ""
// for none.
func msStamp(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}

// openCodePart is a part row's data as OpenCode's SDK types it (Part).
type openCodePart struct {
	Type        string          `json:"type"`
	Text        *string         `json:"text"`
	Synthetic   bool            `json:"synthetic"`
	Ignored     bool            `json:"ignored"`
	Tool        string          `json:"tool"`
	State       *openCodeState  `json:"state"`
	Filename    string          `json:"filename"`
	Mime        string          `json:"mime"`
	Name        string          `json:"name"`
	Agent       string          `json:"agent"`
	Description string          `json:"description"`
	Attempt     *int64          `json:"attempt"`
	Error       json.RawMessage `json:"error"`
}

// openCodeState is a tool part's state: pending, running, completed or error.
type openCodeState struct {
	Status string          `json:"status"`
	Input  json.RawMessage `json:"input"`
	Output *string         `json:"output"`
	Error  *string         `json:"error"`
	Time   struct {
		Start *float64 `json:"start"`
		End   *float64 `json:"end"`
	} `json:"time"`
}

// parseOpenCode reads one session of one store into events.
func parseOpenCode(ctx context.Context, storePath, id string) (*parsed, error) {
	locator := openCodeLocator(storePath, id)
	rows, err := index.ReadOpenCodeSessionRows(ctx, filepath.Dir(storePath), id)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, runtimeFailure("cannot read %s: %v", locator, err)
	}
	p := &parsed{
		path: locator, engine: engineOpenCode, disp: map[int]disposition{}, meta: map[string]string{},
		nulls: map[string]bool{}, pending: map[string]*event{}, calls: map[string]*event{}, title: rows.Title,
		messages: len(rows.Messages), parts: len(rows.Parts),
	}
	p.meta["cwd"] = rows.Directory
	p.meta["session"] = rows.ID
	if rows.ParentID != "" {
		p.meta["agent"], p.meta["session"] = rows.ID, rows.ParentID
	}
	partsOf := map[string][]index.OpenCodeRow{}
	for _, part := range rows.Parts {
		partsOf[part.MessageID] = append(partsOf[part.MessageID], part)
	}
	n := 0
	for _, row := range rows.Messages {
		n++
		message, ok := p.openCodeMessage(n, row, rows.ParentID != "")
		parts := partsOf[row.ID]
		delete(partsOf, row.ID)
		final := ""
		if ok && message.Role == transcript.RoleAssistant && message.Finish == "stop" {
			final = lastTextPart(parts)
		}
		for _, part := range parts {
			n++
			role := ""
			if ok {
				role = message.Role
			}
			p.openCodePart(n, part, role, part.ID == final)
		}
	}
	for _, part := range rows.Parts {
		if _, orphan := partsOf[part.MessageID]; orphan {
			n++
			p.stamp(msStamp(part.TimeCreatedMS))
			p.mark(n, dispSkipped, "part (no message)")
		}
	}
	p.records = n
	if n == 0 {
		return nil, runtimeFailure("empty transcript: %s", locator)
	}
	return p, nil
}

// lastTextPart is the id of the last text part that renders, "" for none.
func lastTextPart(parts []index.OpenCodeRow) string {
	last := ""
	for _, row := range parts {
		var part openCodePart
		if json.Unmarshal([]byte(row.Data), &part) != nil || part.Type != "text" || part.Ignored || part.Text == nil ||
			pyvalue.Strip(*part.Text) == "" {
			continue
		}
		last = row.ID
	}
	return last
}

// openCodeMessage reads one message record: the model and effort from the
// first assistant message, a child session's agent from its first message, a
// failed turn's error as a note; ok is false when its data could not be read.
func (p *parsed) openCodeMessage(n int, row index.OpenCodeRow, child bool) (index.OpenCodeMessage, bool) {
	ts := msStamp(row.TimeCreatedMS)
	p.stamp(ts)
	message, err := index.DecodeOpenCodeMessage(row.Data)
	if err != nil {
		p.mark(n, dispSkipped, "message (unparseable)")
		return message, false
	}
	if _, set := p.meta["agent_type"]; child && !set && message.Agent != "" {
		p.meta["agent_type"] = message.Agent
	}
	switch message.Role {
	case transcript.RoleAssistant:
		if _, set := p.meta["model"]; !set && message.ModelID != "" {
			p.meta["model"] = joinNonEmpty("/", message.ProviderID, message.ModelID)
			if message.Variant != "" {
				p.meta["effort"] = message.Variant
			}
		}
		if text := openCodeError(message.Error); text != "" {
			p.add(&event{line: n, ts: ts, kind: kindNote, text: "ENGINE ERROR — " + text}, "message.assistant (error)")
			return message, true
		}
		p.mark(n, dispHeader, "message.assistant")
	case transcript.RoleUser:
		p.mark(n, dispHeader, "message.user")
	default:
		p.mark(n, dispSkipped, "message."+typeOr(message.Role))
	}
	return message, true
}

// openCodeError is an error object as "{name}: {data.message}", "" for none.
func openCodeError(raw json.RawMessage) string {
	if !isObject(raw) {
		return ""
	}
	name := stringOr(member(raw, "name"), "")
	text := ""
	if data := member(raw, "data"); isObject(data) {
		text = stringOr(member(data, "message"), "")
	}
	if text == "" {
		text = stringOr(member(raw, "message"), "")
	}
	if out := joinNonEmpty(": ", name, text); out != "" {
		return out
	}
	return pyvalue.Dumps(raw)
}

func typeOr(kind string) string {
	if kind == "" {
		return "?"
	}
	return kind
}

// openCodePart reads one part record of a message of role (empty when the
// message could not be read); final marks the text part that ends the turn.
func (p *parsed) openCodePart(n int, row index.OpenCodeRow, role string, final bool) {
	ts := msStamp(row.TimeCreatedMS)
	p.stamp(ts)
	var part openCodePart
	if err := json.Unmarshal([]byte(row.Data), &part); err != nil {
		p.mark(n, dispSkipped, "part (unparseable)")
		return
	}
	label := "part." + typeOr(part.Type)
	if role == "" {
		p.mark(n, dispSkipped, label+" (of an unreadable message)")
		return
	}
	note := func(text string) { p.add(&event{line: n, ts: ts, kind: kindNote, text: text}, label) }
	switch part.Type {
	case "text":
		p.openCodeText(n, ts, label, &part, role, final)
	case "file":
		if role != transcript.RoleUser {
			p.mark(n, dispSkipped, label)
			return
		}
		name := part.Filename
		if name == "" {
			name = typeOr(part.Mime)
		}
		p.add(&event{line: n, ts: ts, kind: kindPrompt, text: "[file " + name + "]"}, label)
	case openCodeToolPart:
		p.add(openCodeCall(n, ts, &part), label)
	case "compaction":
		note(compactedNote)
	case "agent":
		note("AGENT @" + part.Name)
	case "subtask":
		note("SUBTASK " + part.Agent + ": " + part.Description)
	case "retry":
		attempt := "?"
		if part.Attempt != nil {
			attempt = strconv.FormatInt(*part.Attempt, 10)
		}
		note("RETRY " + attempt + " — " + openCodeError(part.Error))
	default:
		p.mark(n, dispSkipped, label)
	}
}

// openCodeText reads a text part: a user's is a prompt (OpenCode's own
// synthetic text a note), an assistant's a reply, the turn's last a final.
func (p *parsed) openCodeText(n int, ts, label string, part *openCodePart, role string, final bool) {
	text := ""
	if part.Text != nil {
		text = pyvalue.Strip(*part.Text)
	}
	switch {
	case part.Ignored:
		p.mark(n, dispSkipped, label+" (ignored)")
	case text == "":
		p.mark(n, dispSkipped, label+" (empty)")
	case role == transcript.RoleUser && part.Synthetic:
		p.add(&event{line: n, ts: ts, kind: kindNote, text: text}, label+" (synthetic)")
	case role == transcript.RoleUser:
		p.add(&event{line: n, ts: ts, kind: kindPrompt, text: text}, label)
	case role == transcript.RoleAssistant && final:
		p.add(&event{line: n, ts: ts, kind: kindFinal, text: text}, label)
	case role == transcript.RoleAssistant:
		p.add(&event{line: n, ts: ts, kind: kindReply, text: text}, label)
	default:
		p.mark(n, dispSkipped, label)
	}
}

// openCodeCall is a tool part as a call: a completed state's output is its
// result, an error state's error a failed result, a pending or running state
// none; its duration is the state's own span.
func openCodeCall(n int, ts string, part *openCodePart) *event {
	name := typeOr(part.Tool)
	ev := &event{line: n, ts: ts, kind: kindCall, name: name, cmd: name == "bash"}
	state := part.State
	if state == nil {
		ev.text = "None"
		return ev
	}
	ev.text, ev.raw = openCodeTarget(name, state.Input), pyvalue.Dumps(state.Input)
	switch state.Status {
	case "completed":
		output := ""
		if state.Output != nil {
			output = *state.Output
		}
		ev.result = &output
	case openCodeStateError:
		failure := ""
		if state.Error != nil {
			failure = *state.Error
		}
		ev.result, ev.err = &failure, true
	}
	if ev.result != nil && state.Time.Start != nil && state.Time.End != nil {
		seconds := (*state.Time.End - *state.Time.Start) / 1000
		ev.dur = &seconds
	}
	return ev
}

// openCodeTarget is what an OpenCode tool call acts on, as one line names it.
func openCodeTarget(name string, input json.RawMessage) string {
	if !isObject(input) {
		return stringOr(input, "None")
	}
	get := func(key string) json.RawMessage { return member(input, key) }
	switch name {
	case "bash":
		return stringOr(get("command"), "")
	case openCodeReadTool, "write", openCodeEditTool, "multiedit":
		path := stringOr(get("filePath"), "")
		if name == openCodeReadTool && (pyvalue.Bool(get("offset")) || pyvalue.Bool(get("limit"))) {
			return path + " @" + stringOr(get("offset"), "1") + "+" + stringOr(get("limit"), "")
		}
		if name == openCodeEditTool {
			old, updated := stringOr(get("oldString"), ""), stringOr(get("newString"), "")
			return path + " (-" + strconv.Itoa(strings.Count(old, "\n")+1) + " +" +
				strconv.Itoa(strings.Count(updated, "\n")+1) + " lines)"
		}
		return path
	case "grep", "glob", "list":
		var parts []string
		for _, key := range []string{"pattern", "path", "include"} {
			if pyvalue.Bool(get(key)) {
				parts = append(parts, pyvalue.Str(get(key)))
			}
		}
		return strings.Join(parts, " ")
	case "task":
		return stringOr(get("subagent_type"), "") + ": " + stringOr(get("description"), "")
	case "apply_patch", "patch":
		if patch := get("patchText"); patch != nil {
			return codexTarget("apply_patch", patch)
		}
	}
	for _, key := range []string{"url", "query", "name"} {
		if pyvalue.Bool(get(key)) {
			return pyvalue.Str(get(key))
		}
	}
	return pyvalue.Dumps(input)
}

// openCodeAgents lists a session's child sessions, theirs included: each by
// its id, agent, title, span, records and tool calls, the earliest first.
func openCodeAgents(ctx context.Context, storePath, id string) (string, error) {
	root := filepath.Dir(storePath)
	locator := openCodeLocator(storePath, id)
	sessions, err := index.ReadOpenCodeSessions(ctx, root)
	if err != nil {
		return "", runtimeFailure("cannot read %s: %v", storePath, err)
	}
	known, children := false, map[string][]store.OpenCodeSession{}
	for at := range sessions {
		session := &sessions[at]
		known = known || session.ID == id
		children[session.ParentID] = append(children[session.ParentID], *session)
	}
	if !known {
		return "", runtimeFailure("cannot read %s: %v", locator,
			fmt.Errorf("%w %s in %s", index.ErrNoOpenCodeSession, id, storePath))
	}
	lines := []string{"SESSION " + id + " · " + locator}
	var rows []agentRow
	seen := map[string]bool{id: true}
	for pending := append([]store.OpenCodeSession(nil), children[id]...); len(pending) != 0; pending = pending[1:] {
		child := pending[0]
		if seen[child.ID] {
			continue
		}
		seen[child.ID] = true
		rows = append(rows, readOpenCodeAgentRow(ctx, root, child))
		pending = append(pending, children[child.ID]...)
	}
	if len(rows) == 0 {
		return strings.Join(append(lines, "AGENTS 0 — no child session of "+id+" in "+storePath), "\n") + "\n", nil
	}
	sortAgentRows(rows)
	lines = append(lines, fmt.Sprintf("AGENTS %d", len(rows)))
	for _, row := range rows {
		lines = append(lines, row.text)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// readOpenCodeAgentRow reads one child session's rows into its agents line.
func readOpenCodeAgentRow(ctx context.Context, root string, child store.OpenCodeSession) agentRow {
	rows, err := index.ReadOpenCodeSessionRows(ctx, root, child.ID)
	if err != nil {
		return agentRow{text: child.ID + " · READ ERROR " + err.Error()}
	}
	var first, last *time.Time
	calls := 0
	note := func(ms int64) {
		at, ok := parseStamp(msStamp(ms))
		if !ok {
			return
		}
		if first == nil || at.Before(*first) {
			first = &at
		}
		if last == nil || at.After(*last) {
			last = &at
		}
	}
	for _, message := range rows.Messages {
		note(message.TimeCreatedMS)
	}
	for _, row := range rows.Parts {
		note(row.TimeCreatedMS)
		var part openCodePart
		if json.Unmarshal([]byte(row.Data), &part) == nil && part.Type == openCodeToolPart {
			calls++
		}
	}
	span := noTimestamps
	if first != nil {
		span = first.Format("2006-01-02 15:04:05") + "Z → " + last.Format("2006-01-02 15:04:05") + "Z"
	}
	agent, title := child.Agent, child.Title
	if agent == "" {
		agent = "agent ?"
	}
	if title == "" {
		title = "no title"
	}
	return agentRow{first: first, text: fmt.Sprintf("%s · %s · %s · %s · %d records · %d calls", child.ID, agent,
		title, span, len(rows.Messages)+len(rows.Parts), calls)}
}

// openCodeNameRows are the AMBIGUOUS rows of OpenCode sessions a name matched.
func openCodeNameRows(hits []openCodeHit) []string {
	rows := make([]string, len(hits))
	for row := range hits {
		hit := &hits[row]
		updated := "never updated"
		if at, ok := parseStamp(msStamp(hit.session.TimeUpdatedMS)); ok {
			updated = "updated " + at.Format("2006-01-02 15:04:05") + "Z"
		}
		rows[row] = hit.locator() + " (OpenCode session, " + updated + ")"
	}
	return rows
}
