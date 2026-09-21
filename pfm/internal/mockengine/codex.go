package mockengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
)

// mcpTimeout bounds the mock's own MCP handshake against a dead or hanging
// endpoint — the same discipline hooks.go:162 applies to hook commands. A
// var, not a const, so a test can shrink it rather than block for 30s proving
// a hang is eventually bounded.
var mcpTimeout = 30 * time.Second

// mcpBoundedContext is the context session.mcp connects and lists tools
// under: an mcp.StreamableClientTransport with MaxRetries: -1 has no bound of
// its own, so a dead or hanging endpoint would otherwise block the mock
// forever — the same discipline hooks.go:162 applies to hook commands.
func mcpBoundedContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, mcpTimeout)
}

// codexInvocation is the Codex command line pfm emits (internal/action for
// the TUI, internal/reload/reload.go:638 `resume "<id>"`) plus the doors the
// mock refuses by name.
type codexInvocation struct {
	subcommand string
	model      string
	threadID   string
	resumed    bool
	prompt     string
}

var codexValueFlags = map[string]bool{
	flagModel: true, "-m": true, "-c": true, "--config": true, "--sandbox": true, "-s": true, "-a": true,
	"--ask-for-approval": true, "--profile": true, "-p": true, "--cd": true, "-C": true, "--image": true, "-i": true,
}

func parseCodexArgs(args []string) codexInvocation {
	var call codexInvocation
	for index := 0; index < len(args); {
		argument := args[index]
		if !strings.HasPrefix(argument, "-") {
			switch {
			case call.subcommand == "" && call.prompt == "" &&
				(argument == sourceResume || argument == "exec" || argument == "app-server" || argument == "mcp-server"):
				call.subcommand = argument
			case call.subcommand == sourceResume && call.threadID == "":
				call.threadID = argument
				call.resumed = true
			case call.prompt == "":
				call.prompt = argument
			}
			index++
			continue
		}
		name, inline, hasInline := strings.Cut(argument, "=")
		value, next := inline, index+1
		if codexValueFlags[name] && !hasInline && next < len(args) {
			value = args[next]
			next++
		}
		if name == flagModel || name == "-m" {
			call.model = value
		}
		index = next
	}
	return call
}

// codexSession is the Codex engine behind the shared pane loop: one thread's
// rollout, its session_index rows, hooks.json and the MCP client.
type codexSession struct {
	proc      *process
	call      codexInvocation
	codexHome string
	threadID  string
	rollout   string
	model     string
	hooks     hookSet
	environ   []string
	seat      *seat
}

func serveCodex(proc *process) int {
	call := parseCodexArgs(proc.args)
	switch call.subcommand {
	case "exec":
		warn(proc.stderr, "codex exec JSONL events are unpinned in this mock — internal/headless/run/run.go:773 "+
			"reads them; a scenario door lands with the Tier B capture")
		return ExitUnpinned
	case "app-server", "mcp-server":
		warn(proc.stderr, "codex %s JSON-RPC is unpinned in this mock — internal/statusline/refresh_codex.go:87 "+
			"reads rateLimits; a scenario door lands with the Tier B capture", call.subcommand)
		return ExitUnpinned
	}
	codexHome := proc.env("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(proc.env("HOME"), ".codex")
	}
	hooks, _, err := loadHookDocument(filepath.Join(codexHome, "hooks.json"))
	if err != nil {
		warn(proc.stderr, "%v", err)
		return ExitUsage
	}
	session := &codexSession{
		proc: proc, call: call, codexHome: codexHome, model: proc.script.Model, hooks: hooks, environ: os.Environ(),
	}
	if call.model != "" {
		session.model = call.model
	}
	session.threadID = call.threadID
	if session.threadID == "" {
		session.threadID = proc.script.SessionID
	}
	if session.threadID == "" {
		session.threadID = newUUID()
	}
	return runPane(proc, session, call.prompt, call.resumed)
}

func (session *codexSession) composerGlyph() string { return "›" }

// busyLine and compactedLine are the scenario's words: Codex's own are UNPINNED
// (testdata/shapes/codex/*.txt) and runPane refuses to render without them.
func (session *codexSession) busyLine(time.Duration, Tokens) string {
	return session.proc.script.Pane.Busy
}

func (session *codexSession) compactedLine() string { return session.proc.script.Pane.Compacted }

// Rollout record types internal/index/codex.go:82-113 reads.
const (
	recordResponseItem = "response_item"
	recordEventMsg     = "event_msg"
	payloadMessage     = "message"
)

// codexRecord is one rollout line: internal/codexmeta/header.go:37-40's
// {timestamp,type,payload}.
type codexRecord struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   any    `json:"payload"`
}

// codexMessage is a response_item message in the block shape
// internal/naming.FlattenPromptText and internal/transcript.Visible read.
type codexMessage struct {
	Type    string         `json:"type"`
	Role    string         `json:"role"`
	Content []codexContent `json:"content"`
}

type codexContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (session *codexSession) write(record codexRecord) error {
	if record.Timestamp == "" {
		record.Timestamp = stamp(time.Now())
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode rollout record: %w", err)
	}
	return appendLine(session.rollout, encoded)
}

// rolloutFor is the rollout path for a thread: sessions/YYYY/MM/DD/
// rollout-<stamp>-<thread>.jsonl, the name index/walk.go:152 strips the id off.
func (session *codexSession) rolloutFor(threadID string, now time.Time) string {
	now = now.UTC()
	return filepath.Join(
		session.codexHome, "sessions", now.Format("2006"), now.Format("01"), now.Format("02"),
		"rollout-"+now.Format("2006-01-02T15-04-05")+"-"+threadID+".jsonl",
	)
}

// findRollout locates an existing thread's rollout for resume.
func (session *codexSession) findRollout(threadID string) (string, error) {
	found := ""
	err := filepath.WalkDir(
		filepath.Join(session.codexHome, "sessions"),
		func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), threadID+".jsonl") {
				found = path
			}
			return nil
		},
	)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("find rollout for %s: %w", threadID, err)
	}
	return found, nil
}

func (session *codexSession) sessionMeta(threadID, parent string, now time.Time) codexRecord {
	payload := map[string]any{
		"id": threadID, "timestamp": stamp(now), keyCWD: session.proc.cwd, "originator": "codex_cli_rs",
		"cli_version": strings.TrimPrefix(session.proc.script.Version, "codex-cli "), "source": "cli",
		"thread_source": roleUser,
	}
	if parent != "" {
		payload["thread_source"] = "subagent"
		payload["parent_thread_id"] = parent
		payload["source"] = map[string]any{
			"subagent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": parent}},
		}
	}
	return codexRecord{Timestamp: stamp(now), Type: "session_meta", Payload: payload}
}

func (session *codexSession) start(resumed bool) error {
	now := time.Now()
	source := sourceStartup
	if resumed {
		source = sourceResume
		existing, err := session.findRollout(session.threadID)
		if err != nil {
			return err
		}
		session.rollout = existing
	}
	if session.rollout == "" {
		session.rollout = session.rolloutFor(session.threadID, now)
		if err := session.write(session.sessionMeta(session.threadID, "", now)); err != nil {
			return err
		}
	}
	if err := session.write(session.turnContext()); err != nil {
		return err
	}
	seat, err := bindSeat(session.proc, engineCodex, session.rollout)
	if err != nil {
		return err
	}
	session.seat = seat
	// hooks.json SessionStart, stdin per Codex's own hook input contract.
	payload := map[string]any{
		"hook_event_name": hookSessionStart, "source": source, "transcript_path": session.rollout,
		"session_id": session.threadID, keyCWD: session.proc.cwd,
	}
	answers, err := session.hooks.fire(
		session.proc.ctx,
		hookSessionStart,
		source,
		payload,
		session.proc.cwd,
		session.environ,
	)
	if err != nil {
		return err
	}
	for index := range answers {
		additional := answers[index].Specific.AdditionalContext
		if additional == "" {
			continue
		}
		// Codex folds additionalContext into history as a developer message.
		if err := session.write(codexRecord{Type: recordResponseItem, Payload: codexMessage{
			Type:    payloadMessage,
			Role:    roleDeveloper,
			Content: []codexContent{{Type: blockInputText, Text: additional}},
		}}); err != nil {
			return err
		}
	}
	return nil
}

// turnContext carries the model internal/transcript/meta.go:139 reads.
func (session *codexSession) turnContext() codexRecord {
	return codexRecord{Type: "turn_context", Payload: map[string]any{
		keyCWD: session.proc.cwd, "approval_policy": "never", "model": session.model, "summary": "auto",
	}}
}

func (session *codexSession) prompt(string) (string, error) { return "", nil }

func (session *codexSession) recordUser(text string) error {
	return session.write(codexRecord{Type: recordResponseItem, Payload: codexMessage{
		Type: payloadMessage, Role: roleUser, Content: []codexContent{{Type: blockInputText, Text: text}},
	}})
}

func (session *codexSession) recordAssistant(reply string, usage Tokens) error {
	records := []codexRecord{
		{Type: recordResponseItem, Payload: codexMessage{
			Type: payloadMessage, Role: roleAssistant, Content: []codexContent{{Type: blockOutputText, Text: reply}},
		}},
		{Type: recordEventMsg, Payload: map[string]any{keyType: "agent_message", payloadMessage: reply}},
		{Type: recordEventMsg, Payload: map[string]any{keyType: "token_count", "info": map[string]any{
			"total_token_usage": map[string]any{
				"input_tokens": usage.Input, "output_tokens": usage.Output, "total_tokens": usage.Input + usage.Output,
			},
			"last_token_usage": map[string]any{
				"input_tokens": usage.Input, "output_tokens": usage.Output, "total_tokens": usage.Input + usage.Output,
			},
			"model_context_window": usage.ContextWindow,
		}}},
	}
	for index := range records {
		if err := session.write(records[index]); err != nil {
			return err
		}
	}
	return nil
}

func (session *codexSession) tool(step Step) (string, error) {
	arguments := string(step.Input)
	if arguments == "" {
		arguments = "{}"
	}
	callID := "call_" + newUUID()[:8]
	records := []codexRecord{
		{Type: recordResponseItem, Payload: map[string]any{
			keyType: "function_call", "name": step.Tool, "arguments": arguments, "call_id": callID,
		}},
		{Type: recordResponseItem, Payload: map[string]any{
			keyType: "function_call_output", "call_id": callID, "output": "done",
		}},
	}
	for index := range records {
		if err := session.write(records[index]); err != nil {
			return "", err
		}
	}
	return "", nil
}

// compact writes the `compacted` record a reader of the rollout sees: the
// replacement history is the summary the fixture supplies.
func (session *codexSession) compact(step Step) error {
	history := []codexMessage{{
		Type: payloadMessage, Role: roleUser,
		Content: []codexContent{{Type: blockInputText, Text: "Summary of the thread so far (fixture)."}},
	}}
	return session.write(codexRecord{Type: "compacted", Payload: map[string]any{
		payloadMessage:        "Summary of the thread so far (fixture).",
		"replacement_history": history,
	}})
}

// rename appends the session_index.jsonl row internal/codexmeta/session_index.go
// decodes: id, thread_name, updated_at.
func (session *codexSession) rename(name string) error {
	entry := codexmeta.SessionIndexEntry{
		ID:         session.threadID,
		ThreadName: name,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode session index row: %w", err)
	}
	return appendLine(filepath.Join(session.codexHome, codexmeta.SessionIndexFile), encoded)
}

// background spawns a subagent thread: its own rollout whose session_meta
// names this thread as parent (internal/codexmeta/header.go:71-84 reads it).
// Codex's pane shows nothing pfm pins for it, so no row is rendered.
func (session *codexSession) background(step Step) (string, error) {
	child := &codexSession{proc: session.proc, codexHome: session.codexHome, threadID: newUUID(), model: session.model}
	now := time.Now()
	child.rollout = child.rolloutFor(child.threadID, now)
	if err := child.write(session.sessionMeta(child.threadID, session.threadID, now)); err != nil {
		return "", err
	}
	if err := child.recordUser("Subagent task for " + step.Name); err != nil {
		return "", err
	}
	return "", child.recordAssistant(step.Status, session.proc.script.Tokens)
}

// mcp connects to the [mcp_servers.<server>] block pfm wrote into config.toml
// (internal/installer/mcp.go:229-241) and performs initialize + tools/list,
// recording the tool names for the test to compare against the server's.
func (session *codexSession) mcp(step Step) error {
	name := step.Server
	if name == "" {
		name = "chat"
	}
	var config struct {
		Servers map[string]struct {
			URL string `toml:"url"`
		} `toml:"mcp_servers"`
	}
	path := filepath.Join(session.codexHome, "config.toml")
	if _, err := toml.DecodeFile(path, &config); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	server, ok := config.Servers[name]
	if !ok || server.URL == "" {
		return fmt.Errorf("%s has no [mcp_servers.%s] url", path, name)
	}
	bounded, cancel := mcpBoundedContext(session.proc.ctx)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "mock-engine", Version: session.proc.script.Version}, nil)
	connection, err := client.Connect(
		bounded,
		&mcp.StreamableClientTransport{Endpoint: server.URL, MaxRetries: -1, DisableStandaloneSSE: true},
		nil,
	)
	if err != nil {
		return fmt.Errorf("initialize against %s: %w", server.URL, err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			warn(session.proc.stderr, "close MCP session: %v", err)
		}
	}()
	tools, err := connection.ListTools(bounded, nil)
	if err != nil {
		return fmt.Errorf("tools/list against %s: %w", server.URL, err)
	}
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	encoded, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("encode tool names: %w", err)
	}
	return session.proc.recorder.write("mcp-"+name+".json", string(encoded)+"\n")
}

func (session *codexSession) clear() error {
	return errors.New("codex has no /clear in pfm's model of it (internal/reload drives resume, never clear)")
}

func (session *codexSession) finish(string) error {
	if session.seat == nil {
		return nil
	}
	err := session.seat.release()
	session.seat = nil
	return err
}

// statusLine is empty: Codex's status row is unpinned pane text.
func (session *codexSession) statusLine(Tokens) (string, error) { return "", nil }
