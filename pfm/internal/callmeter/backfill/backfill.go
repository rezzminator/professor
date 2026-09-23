// Package backfill builds callmeter rows from the Claude transcripts already
// on disk (docs/design/hooks/callmeter.md § Backfill): every
// {configDir}/projects/**/*.jsonl, sub-agent transcripts included, upserted with
// callmeter.FillEmpty so a value the hook recorded is never overwritten — save
// a sub-agent's totals, which are always its whole transcript's sums, the
// hook's rule at SubagentStop.
package backfill

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
	"github.com/rezzminator/professor/pfm/internal/clock"
)

// Logf receives one line per fault and per config dir without projects/.
type Logf func(format string, args ...any)

// Problem is one transcript that could not be read (Line 0), or one malformed
// line of a transcript that was read.
type Problem struct {
	Path string
	Line int
	Err  string
}

// FromTranscripts walks projects/ of every config dir in configDirs and upserts, one
// transaction per transcript, the calls, requests and agents its tool entries
// at or after since describe (a zero since reads every entry). A transcript
// that cannot be read and a malformed line are backfill faults in the store and
// Problems in the Summary, and the walk goes on; the error is a store that
// failed a write or a cancelled ctx, returned with the Summary so far.
func FromTranscripts(
	ctx context.Context,
	store *callmeter.Store,
	configDirs []string,
	since time.Time,
	logf Logf,
) (Summary, error) {
	if store == nil {
		return Summary{}, errors.New("callmeter backfill: no store")
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	r := &runner{
		store:    store,
		since:    since,
		logf:     logf,
		clock:    clock.Real,
		requests: map[string]bool{},
		agents:   map[string]bool{},
	}
	for _, dir := range configDirs {
		if err := r.configDir(ctx, dir); err != nil {
			return r.sum, err
		}
	}
	err := store.Batch(ctx, func(tx *callmeter.Tx) error {
		for _, a := range r.totals {
			if !r.agents[a.AgentID] {
				continue // no row this run: an agent wholly outside the window
			}
			// The transcript's sums hold every turn the agent ran and overwrite
			// the parent's Agent result (its first turn); its model and stop
			// only fill, so the result's resolved model and a hook's stop stay.
			fill := callmeter.Agent{AgentID: a.AgentID, Model: a.Model, Stopped: a.Stopped}
			a.Model, a.Stopped = nil, nil
			if err := tx.UpsertAgent(ctx, a, callmeter.Overwrite); err != nil {
				return err
			}
			if err := tx.UpsertAgent(ctx, fill, callmeter.FillEmpty); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return r.sum, fmt.Errorf("callmeter backfill: agent totals: %w", err)
	}
	if r.sum.Retired, err = retireOrphans(ctx, store, configDirs, r.clock.Now(), logf); err != nil {
		return r.sum, err
	}
	return r.sum, nil
}

type runner struct {
	store *callmeter.Store
	since time.Time
	logf  Logf
	clock clock.Clock // stamps each fault
	sum   Summary
	// requests and agents are the keys upserted so far: a sub-agent is written
	// from its parent's Agent result and from its own transcript, counted once.
	requests map[string]bool
	agents   map[string]bool
	// totals are the sub-agent sums read from their own transcripts, written
	// after every transcript over the parent Agent result's totals, as the
	// hook's SubagentStop does.
	totals []callmeter.Agent
}

func (r *runner) configDir(ctx context.Context, dir string) error {
	projects := filepath.Join(dir, "projects")
	info, err := os.Stat(projects)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.sum.NoProjects = append(r.sum.NoProjects, dir)
		r.logf("callmeter backfill: no projects/ in config dir %s", dir)
		return nil
	case err != nil:
		return r.unreadable(ctx, projects, err)
	case !info.IsDir():
		return r.unreadable(ctx, projects, errors.New("not a directory"))
	}
	r.sum.ConfigDirs = append(r.sum.ConfigDirs, dir)
	return filepath.WalkDir(projects, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("callmeter backfill: %w", err)
		}
		if walkErr != nil {
			if err := r.unreadable(ctx, path, walkErr); err != nil {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		if err := r.transcript(ctx, path); err != nil {
			return err
		}
		if d.IsDir() {
			return fs.SkipDir // a directory named *.jsonl, already reported unreadable
		}
		return nil
	})
}

// transcript reads and writes one transcript; the error is a store failure.
func (r *runner) transcript(ctx context.Context, path string) error {
	src := sourceOf(path)
	if src.agentID != "" {
		agentType, err := subagentMeta(path)
		if err != nil {
			if ferr := r.malformed(ctx, Problem{Path: path, Err: err.Error()}); ferr != nil {
				return ferr
			}
		}
		src.agentType = agentType
	}
	out, err := readTranscript(src, r.since)
	if err != nil {
		return r.unreadable(ctx, path, err)
	}
	r.sum.TranscriptsRead++
	r.totals = append(r.totals, out.totals...)
	r.sum.EntriesSkipped += out.skipped
	if err := r.write(ctx, path, out); err != nil {
		return err
	}
	for _, p := range out.malformed {
		if err := r.malformed(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

func (r *runner) write(ctx context.Context, path string, out *rows) error {
	calls := make([]*callmeter.Call, 0, len(out.callOrder))
	for _, id := range out.callOrder {
		calls = append(calls, out.calls[id])
	}
	inserted, filled, err := countCallChanges(ctx, r.store, calls)
	if err != nil {
		return fmt.Errorf("callmeter backfill %s: %w", path, err)
	}
	pending, err := storedPending(ctx, r.store, calls)
	if err != nil {
		return fmt.Errorf("callmeter backfill %s: %w", path, err)
	}
	err = r.store.Batch(ctx, func(tx *callmeter.Tx) error {
		// A request the hook left pending resolves first, as its
		// resolvePending does, so the message row below only fills the merge.
		for _, provisional := range pending.order {
			found := out.requests[pending.message[provisional]]
			resolved := callmeter.Request{
				RequestID:     found.RequestID,
				TS:            found.TS,
				ContextTokens: found.ContextTokens,
				OutputTokens:  found.OutputTokens,
			}
			if err := tx.ResolveRequest(ctx, provisional, resolved); err != nil {
				return err
			}
		}
		for _, c := range calls {
			if err := tx.UpsertCall(ctx, *c, callmeter.FillEmpty); err != nil {
				return err
			}
		}
		for _, id := range out.reqOrder {
			if err := tx.UpsertRequest(ctx, *out.requests[id], callmeter.FillEmpty); err != nil {
				return err
			}
		}
		for _, id := range out.agentOrd {
			if err := tx.UpsertAgent(ctx, *out.agents[id], callmeter.FillEmpty); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("callmeter backfill %s: %w", path, err)
	}
	r.sum.CallsInserted += inserted
	r.sum.CallsFilled += filled
	for _, id := range out.reqOrder {
		r.requests[id] = true
	}
	for _, id := range out.agentOrd {
		r.agents[id] = true
	}
	r.sum.Requests, r.sum.Agents = len(r.requests), len(r.agents)
	return nil
}

func (r *runner) unreadable(ctx context.Context, path string, cause error) error {
	p := Problem{Path: path, Err: cause.Error()}
	r.sum.Unreadable = append(r.sum.Unreadable, p)
	return r.fault(ctx, fmt.Sprintf("unreadable transcript %s: %s", path, p.Err))
}

func (r *runner) malformed(ctx context.Context, p Problem) error {
	r.sum.Malformed = append(r.sum.Malformed, p)
	if p.Line == 0 {
		return r.fault(ctx, fmt.Sprintf("%s: %s", p.Path, p.Err))
	}
	return r.fault(ctx, fmt.Sprintf("%s line %d: %s", p.Path, p.Line, p.Err))
}

// fault logs text and records it as a backfill fault; the error is a store
// that could not take it.
func (r *runner) fault(ctx context.Context, text string) error {
	r.logf("callmeter backfill: %s", text)
	if err := r.store.AddFault(ctx, callmeter.Fault{
		TS:    r.clock.Now().UnixMilli(),
		Stage: callmeter.StageBackfill,
		Error: text,
	}); err != nil {
		return fmt.Errorf("callmeter backfill: record fault (%s): %w", text, err)
	}
	return nil
}

// rows is everything one transcript yields, in first-seen order.
type rows struct {
	calls     map[string]*callmeter.Call
	callOrder []string
	requests  map[string]*callmeter.Request
	reqOrder  []string
	agents    map[string]*callmeter.Agent
	agentOrd  []string
	skipped   int
	malformed []Problem
	totals    []callmeter.Agent // a sub-agent file's own sums, written last (runner.totals)
}

func newRows() *rows {
	return &rows{
		calls:    map[string]*callmeter.Call{},
		requests: map[string]*callmeter.Request{},
		agents:   map[string]*callmeter.Agent{},
	}
}

func (r *rows) agent(id string) *callmeter.Agent {
	if a, ok := r.agents[id]; ok {
		return a
	}
	a := &callmeter.Agent{AgentID: id}
	r.agents[id] = a
	r.agentOrd = append(r.agentOrd, id)
	return a
}

type entry struct {
	Type          string          `json:"type"`
	Timestamp     string          `json:"timestamp"`
	Cwd           string          `json:"cwd"`
	SessionID     string          `json:"sessionId"`
	Message       json.RawMessage `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	// Operation and Content are a "queue-operation" entry's own fields: a
	// task notice enqueued for delivery, at the top level rather than under
	// Message.
	Operation string `json:"operation"`
	Content   string `json:"content"`
}

type message struct {
	ID      string          `json:"id"`
	Content json.RawMessage `json:"content"`
	Usage   *struct {
		InputTokens              int64 `json:"input_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
	} `json:"usage"`
}

type block struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Text      string          `json:"text"`
}

// toolResult is the fields of a toolUseResult object backfill reads for
// itself: a Bash result's output sizes and an Agent/Task result's sub-agent.
// The file columns are callmeter.FileColumnsFromResult's.
type toolResult struct {
	Stdout              *string `json:"stdout"`
	PersistedOutputSize *int64  `json:"persistedOutputSize"`
	PersistedOutputPath *string `json:"persistedOutputPath"`
	Status              string  `json:"status"`
	AgentID             string  `json:"agentId"`
	AgentType           *string `json:"agentType"`
	TotalTokens         *int64  `json:"totalTokens"`
	TotalToolUseCount   *int64  `json:"totalToolUseCount"`
	ResolvedModel       *string `json:"resolvedModel"`
}

// entries older than since. The error is a transcript that could not be read;
// a malformed line is a Problem in rows and the parse goes on. A final line with
// no newline that does not parse is a writer mid-append and is skipped.
func readTranscript(src source, since time.Time) (*rows, error) {
	file, err := os.Open(src.path)
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	out := newRows()
	p := parser{
		src: src, since: since, out: out,
		tools: map[string]string{}, started: map[string]int64{}, badInput: map[string]bool{},
	}
	reader := bufio.NewReaderSize(file, 1<<16)
	for line := 1; ; line++ {
		raw, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, errors.Join(fmt.Errorf("read transcript line %d: %w", line, readErr), file.Close())
		}
		partial := errors.Is(readErr, io.EOF)
		if strings.TrimSpace(string(raw)) != "" {
			if err := p.line(raw); err != nil && !partial {
				out.malformed = append(out.malformed, Problem{Path: src.path, Line: line, Err: err.Error()})
			}
		}
		if partial {
			break
		}
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close transcript: %w", err)
	}
	p.finish()
	return out, nil
}

type parser struct {
	src     source
	since   time.Time
	out     *rows
	tools   map[string]string // tool_use_id -> tool name, calls in the window
	started map[string]int64  // tool_use_id -> ts, calls in the window
	first   *int64            // the earliest in-window entry of a sub-agent file
	// badInput is the tool_use_ids whose input the file columns could not read:
	// failed calls whose error their tool_result never replaces.
	badInput map[string]bool
}

func (p *parser) line(raw []byte) error {
	var e entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return fmt.Errorf("malformed entry: %w", err)
	}
	// A background agent's task notice reaches the parent transcript in
	// whichever shape the harness happened to record it in: most of them are
	// only ever enqueued (the queue-operation itself, absorbed into a turn
	// without ever becoming its own "user" entry), and some do land as a
	// plain-text "user" entry. Both carry the same <task-notification> text.
	if e.Type == "queue-operation" {
		if e.Operation == "enqueue" && strings.HasPrefix(e.Content, "<task-notification>") {
			if err := p.taskNotice(e.Content, e.Timestamp, "queue-operation"); err != nil {
				return err
			}
		}
		return nil
	}
	if (e.Type != "assistant" && e.Type != "user") || len(e.Message) == 0 {
		return nil
	}
	var m message
	if err := json.Unmarshal(e.Message, &m); err != nil {
		return fmt.Errorf("malformed %s message: %w", e.Type, err)
	}
	if e.Type == "user" && len(m.Content) > 0 && m.Content[0] == '"' {
		var s string
		if err := json.Unmarshal(m.Content, &s); err == nil && strings.HasPrefix(s, "<task-notification>") {
			return p.taskNotice(s, e.Timestamp, "user")
		}
	}
	var blocks []block
	if len(m.Content) == 0 || m.Content[0] != '[' {
		return nil // a plain-text message holds no tool call
	}
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return fmt.Errorf("%s message %q: malformed content blocks: %w", e.Type, m.ID, err)
	}
	var relevant bool
	for i := range blocks {
		b := &blocks[i]
		relevant = relevant || b.Type == "tool_use" || b.Type == "tool_result"
	}
	if !relevant {
		return nil
	}
	at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return fmt.Errorf("%s entry: timestamp %q: %w", e.Type, e.Timestamp, err)
	}
	if !p.since.IsZero() && at.Before(p.since) {
		p.out.skipped++
		return nil
	}
	ts := at.UnixMilli()
	if p.first == nil || ts < *p.first {
		p.first = callmeter.Ptr(ts)
	}
	session := e.SessionID
	if session == "" {
		session = p.src.session
	}
	if e.Type == "assistant" {
		return p.assistant(e, m, blocks, ts, session)
	}
	return p.user(e, blocks, ts, session)
}

func (p *parser) assistant(e entry, m message, blocks []block, ts int64, session string) error {
	var problems []error
	for i := range blocks {
		b := &blocks[i]
		if b.Type != "tool_use" || b.ID == "" {
			continue
		}
		c, ok := p.out.calls[b.ID]
		if !ok {
			c = &callmeter.Call{ToolUseID: b.ID}
			p.out.calls[b.ID] = c
			p.out.callOrder = append(p.out.callOrder, b.ID)
		}
		p.tools[b.ID] = b.Name
		p.started[b.ID] = ts
		c.SessionID = nonEmptyPtr(session)
		c.AgentID = nonEmptyPtr(p.src.agentID)
		c.AgentType = p.src.agentType
		c.TS = callmeter.Ptr(ts)
		c.Tool = nonEmptyPtr(b.Name)
		c.Cwd = nonEmptyPtr(e.Cwd)
		c.Source = callmeter.Ptr(callmeter.SourceTranscript)
		c.ConfigDir = nonEmptyPtr(p.src.configDir)
		if len(b.Input) > 0 {
			input, err := callmeter.SanitizeInput(b.Name, b.Input)
			if err != nil {
				problems = append(problems, fmt.Errorf("call %s: %w", b.ID, err))
			} else {
				c.Input = callmeter.Ptr(input)
			}
		}
		// An input the harness rejected is a failed call, not a lost line
		// (docs/design/hooks/callmeter.md § "Malformed calls are calls").
		err := callmeter.FileColumnsFromInput(c, b.Name, b.Input, e.Cwd)
		p.badInput[b.ID] = err != nil
		if err != nil {
			c.Failed = callmeter.Ptr(true)
			c.Error = callmeter.Ptr("malformed input: " + err.Error())
		}
		if m.ID == "" {
			continue
		}
		c.RequestID = callmeter.Ptr(m.ID)
		r, ok := p.out.requests[m.ID]
		if !ok {
			r = &callmeter.Request{
				RequestID: m.ID,
				SessionID: nonEmptyPtr(session),
				AgentID:   nonEmptyPtr(p.src.agentID),
				TS:        callmeter.Ptr(ts),
				Calls:     callmeter.Ptr(int64(0)),
				Pending:   callmeter.Ptr(false),
				Source:    callmeter.Ptr(callmeter.SourceTranscript),
				ConfigDir: nonEmptyPtr(p.src.configDir),
			}
			p.out.requests[m.ID] = r
			p.out.reqOrder = append(p.out.reqOrder, m.ID)
		}
		*r.Calls++
		if m.Usage != nil {
			u := m.Usage
			r.ContextTokens = callmeter.Ptr(u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens)
			r.OutputTokens = callmeter.Ptr(u.OutputTokens)
		}
	}
	return errors.Join(problems...)
}

func (p *parser) user(e entry, blocks []block, ts int64, session string) error {
	var problems []error
	for i := range blocks {
		b := &blocks[i]
		if b.Type != "tool_result" {
			continue
		}
		c, ok := p.out.calls[b.ToolUseID]
		if !ok {
			continue // its call is outside the window or in another file
		}
		delivered, err := callmeter.DeliveredBytes(b.Content)
		if err != nil {
			problems = append(problems, fmt.Errorf("result of %s: %w", b.ToolUseID, err))
		} else {
			c.BytesDelivered = callmeter.Ptr(delivered)
		}
		if !p.badInput[b.ToolUseID] { // a malformed input keeps its failed and error
			c.Failed = callmeter.Ptr(b.IsError)
			if b.IsError {
				text, err := resultText(b.Content)
				if err == nil && text != "" {
					c.Error = callmeter.Ptr(text)
				}
				// A failed Bash call's output is this text, as the hook's
				// PostToolUseFailure `error` is: it is the call's real size.
				if err == nil && p.tools[b.ToolUseID] == "Bash" {
					c.BytesReal = callmeter.Ptr(int64(len(text)))
				}
			}
		}
		if len(e.ToolUseResult) == 0 || e.ToolUseResult[0] != '{' {
			continue // absent, or a failed call's plain error string
		}
		var result toolResult
		if err := json.Unmarshal(e.ToolUseResult, &result); err != nil {
			problems = append(problems, fmt.Errorf("toolUseResult of %s: %w", b.ToolUseID, err))
			continue
		}
		if err := callmeter.FileColumnsFromResult(c, p.tools[b.ToolUseID], e.ToolUseResult); err != nil {
			problems = append(problems, fmt.Errorf("toolUseResult of %s: %w", b.ToolUseID, err))
		}
		p.result(b.ToolUseID, c, result, ts, session)
	}
	return errors.Join(problems...)
}

func (p *parser) result(id string, c *callmeter.Call, result toolResult, ts int64, session string) {
	switch p.tools[id] {
	case "Bash":
		switch {
		case result.PersistedOutputSize != nil:
			c.BytesReal = result.PersistedOutputSize
			c.PersistedPath = result.PersistedOutputPath
		case result.Stdout != nil:
			c.BytesReal = callmeter.Ptr(int64(len(*result.Stdout)))
		}
	case "Agent", "Task":
		if result.AgentID == "" {
			return
		}
		a := p.out.agent(result.AgentID)
		a.SessionID = nonEmptyPtr(session)
		a.AgentType = result.AgentType
		a.ParentToolUseID = callmeter.Ptr(id)
		a.Started = callmeter.Ptr(p.started[id])
		if result.Status == "completed" {
			a.Stopped = callmeter.Ptr(ts)
		}
		a.TranscriptPath = callmeter.Ptr(childTranscript(p.src, result.AgentID))
		a.TotalTokens = result.TotalTokens
		a.ToolUses = result.TotalToolUseCount
		a.Model = result.ResolvedModel
		a.Source = callmeter.Ptr(callmeter.SourceTranscript)
		a.ConfigDir = nonEmptyPtr(p.src.configDir)
	}
}

// taskNotice parses a task-notification's own timestamp, applies the since
// window, and files it (parser.notice); kind names the entry shape in a
// timestamp error.
func (p *parser) taskNotice(text, timestamp, kind string) error {
	at, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return fmt.Errorf("%s entry: timestamp %q: %w", kind, timestamp, err)
	}
	if !p.since.IsZero() && at.Before(p.since) {
		p.out.skipped++
		return nil
	}
	p.notice(text, at.UnixMilli())
	return nil
}

// notice reads a background agent's task notice: <task-notification> text
// holding <task-id>{id}</task-id> and <status>completed|failed|killed</status>.
// Claude Code writes stop_reason null on many finished sub-agent transcripts,
// so sumTotals's Final rule never fires for them; this notice is their real
// completion, arriving in the PARENT transcript. Only Stopped is touched, and
// only the latest of several notices wins (docs/design/hooks/callmeter.md).
func (p *parser) notice(text string, ts int64) {
	id := noticeTag(text, "task-id")
	status := noticeTag(text, "status")
	if id == "" {
		return
	}
	switch status {
	case "completed", "failed", "killed":
	default:
		return
	}
	a := p.out.agent(id)
	if a.Stopped == nil || *a.Stopped < ts {
		a.Stopped = callmeter.Ptr(ts)
	}
}

// noticeTag reads <tag>value</tag> out of a task-notification string; "" if
// the tag is absent or unclosed.
func noticeTag(text, tag string) string {
	open := "<" + tag + ">"
	i := strings.Index(text, open)
	if i < 0 {
		return ""
	}
	i += len(open)
	j := strings.Index(text[i:], "</"+tag+">")
	if j < 0 {
		return ""
	}
	return text[i : i+j]
}

// finish adds a sub-agent file's own agent row once its entries are read.
func (p *parser) finish() {
	if p.src.agentID == "" {
		return
	}
	// A sub-agent that never replied has entries but no tool entry in the
	// window: no row of its own, yet its totals (0) fill the row its
	// parent's Agent result wrote.
	if p.first == nil {
		p.sumTotals()
		return
	}
	a := p.out.agent(p.src.agentID)
	a.AgentType = p.src.agentType
	a.Started = p.first
	a.TranscriptPath = callmeter.Ptr(p.src.path)
	a.Source = callmeter.Ptr(callmeter.SourceTranscript)
	a.ConfigDir = nonEmptyPtr(p.src.configDir)
	for _, id := range p.out.callOrder {
		if s := p.out.calls[id].SessionID; s != nil {
			a.SessionID = s
			break
		}
	}
	p.sumTotals()
}

// sumTotals queues this sub-agent file's own totals (runner.totals).
func (p *parser) sumTotals() {
	// A background or nested agent's parent result carries no totals: sum
	// its own transcript, the hook's SubagentStop rule. A malformed line is
	// already a Problem of this file; any other failure is one more.
	sum, err := callmeter.ReadAgentTotals(p.src.path)
	if err != nil {
		if len(p.out.malformed) == 0 {
			p.out.malformed = append(p.out.malformed, Problem{Path: p.src.path, Err: "agent totals: " + err.Error()})
		}
		return
	}
	totals := callmeter.Agent{
		AgentID:     p.src.agentID,
		TotalTokens: callmeter.Ptr(sum.TotalTokens),
		ToolUses:    callmeter.Ptr(sum.ToolUses),
		Model:       nonEmptyPtr(sum.Model),
	}
	// A transcript whose last message ends the turn stopped there; one still
	// running or killed mid-turn has no stop.
	if sum.Final {
		at, err := time.Parse(time.RFC3339Nano, sum.LastTimestamp)
		if err != nil {
			p.out.malformed = append(p.out.malformed, Problem{Path: p.src.path, Err: "agent stop: " + err.Error()})
		} else {
			totals.Stopped = callmeter.Ptr(at.UnixMilli())
		}
	}
	p.out.totals = append(p.out.totals, totals)
}

// resultText is a tool_result's content as the model read it: the string
// itself, or the sum of an array's text blocks — used for the error text of a
// failed call, where callmeter.DeliveredBytes (used for BytesDelivered) only
// needs the length.
func resultText(content json.RawMessage) (string, error) {
	if len(content) == 0 || string(content) == "null" {
		return "", nil
	}
	if content[0] == '"' {
		var s string
		if err := json.Unmarshal(content, &s); err != nil {
			return "", fmt.Errorf("content string: %w", err)
		}
		return s, nil
	}
	var parts []block
	if err := json.Unmarshal(content, &parts); err != nil {
		return "", fmt.Errorf("content blocks: %w", err)
	}
	var text strings.Builder
	for i := range parts {
		if parts[i].Type == "text" {
			text.WriteString(parts[i].Text)
		}
	}
	return text.String(), nil
}

// childTranscript is the transcript of agent id spawned from the file src:
// Claude Code keeps every agent of a session in the session's one flat
// subagents/ directory, however deep the spawn, so a sub-agent's child sits
// beside it; a chat's child is callmeter.SubagentTranscriptPath.
func childTranscript(src source, id string) string {
	if src.agentID != "" {
		return filepath.Join(filepath.Dir(src.path), "agent-"+id+".jsonl")
	}
	return callmeter.SubagentTranscriptPath(src.path, id)
}
