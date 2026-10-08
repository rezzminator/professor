package mockengine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// mcpTimeout bounds the mock's own MCP handshake against a dead or hanging
// endpoint — the same discipline hooks.go:162 applies to hook commands. A
// var, not a const, so a test can shrink it rather than block for 30s proving
// a hang is eventually bounded.
var mcpTimeout = 30 * time.Second

// mcpBoundedContext is the context session.mcp spawns the server under and
// connects and lists tools under: a stdio server that never answers has no
// bound of its own, so a hanging server would otherwise block the mock
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
	ephemeral  bool
}

var codexValueFlags = map[string]bool{
	flagModel: true, "-m": true, "-c": true, "--config": true, "--sandbox": true, "-s": true, "-a": true,
	"--ask-for-approval": true, "--profile": true, "-p": true, "--cd": true, "-C": true, "--image": true, "-i": true,
	"--output-schema": true, "--color": true,
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
		if name == "--ephemeral" {
			call.ephemeral = true
		}
		index = next
	}
	return call
}

// codexSession is the Codex engine behind the shared pane loop: one thread's
// rollout, its session_index rows, hooks.json and the MCP client.
type codexSession struct {
	proc        *process
	call        codexInvocation
	codexHome   string
	threadID    string
	rollout     string
	rolloutFile *os.File
	model       string
	threadName  string
	hooks       hookSet
	environ     []string
	seat        *seat
	mcpError    string
}

func serveCodex(proc *process) int {
	call := parseCodexArgs(proc.args)
	switch call.subcommand {
	case "app-server":
		return codexAppServer(proc)
	case "mcp-server":
		warn(proc.stderr, "codex mcp-server JSON-RPC is unpinned in this mock")
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
	defer func() {
		if err := session.finish("other"); err != nil {
			warn(proc.stderr, "finish codex: %v", err)
		}
	}()
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
	if call.subcommand == "exec" {
		return session.headless()
	}
	return runPane(proc, session, call.prompt, call.resumed)
}

func codexAppServer(proc *process) int {
	if len(proc.script.RateLimits) == 0 {
		warn(proc.stderr, "codex app-server JSON-RPC is unpinned — supply rate_limits in the scenario")
		return ExitUnpinned
	}
	scanner := bufio.NewScanner(proc.stdin)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	encoder := json.NewEncoder(proc.stdout)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		reply := map[string]any{"jsonrpc": "2.0"}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			warn(proc.stderr, "decode app-server request: %v", err)
			reply["id"] = nil
			reply["error"] = map[string]any{"code": -32700, "message": "parse app-server request: " + err.Error()}
		} else {
			if len(request.ID) == 0 {
				continue
			}
			reply["id"] = request.ID
			switch request.Method {
			case "initialize":
				reply["result"] = map[string]any{}
			case "account/rateLimits/read":
				reply["result"] = map[string]any{"rateLimits": proc.script.RateLimits}
			case "hooks/list", "config/value/write":
				result, err := codexHookRPC(proc, request.Method, request.Params)
				if err != nil {
					warn(proc.stderr, "%s: %v", request.Method, err)
					reply["error"] = map[string]any{"code": -32602, "message": err.Error()}
				} else {
					reply["result"] = result
				}
			default:
				reply["error"] = map[string]any{"code": -32601, "message": "unknown method " + request.Method}
			}
		}
		if err := encoder.Encode(reply); err != nil {
			warn(proc.stderr, "write app-server reply: %v", err)
			return ExitUsage
		}
	}
	if err := scanner.Err(); err != nil {
		warn(proc.stderr, "read app-server requests: %v", err)
		return ExitUsage
	}
	return 0
}

func (session *codexSession) headless() int {
	proc := session.proc
	prompt := session.call.prompt
	if prompt == "" {
		content, err := io.ReadAll(io.LimitReader(proc.stdin, 1<<20))
		if err != nil {
			warn(proc.stderr, "read exec prompt: %v", err)
			return ExitUsage
		}
		prompt = strings.TrimSpace(string(content))
	}
	if !session.call.ephemeral {
		if err := session.start(false); err != nil {
			warn(proc.stderr, "start exec: %v", err)
			return ExitUsage
		}
		if err := session.recordUser(prompt); err != nil {
			warn(proc.stderr, "record exec prompt: %v", err)
			return ExitUsage
		}
		if err := session.rename(firstWords(prompt)); err != nil {
			warn(proc.stderr, "name exec session: %v", err)
			return ExitUsage
		}
	}
	encoder := json.NewEncoder(proc.stdout)
	for _, event := range []map[string]any{{keyType: "thread.started", "thread_id": session.threadID}, {keyType: "turn.started"}} {
		if err := encoder.Encode(event); err != nil {
			warn(proc.stderr, "write exec start: %v", err)
			return ExitUsage
		}
	}
	running := proc.script.forPrompt(prompt)
	step := running.next()
	for !step.terminal() {
		if step.sideEffecting() {
			warn(proc.stderr, "codex exec fast-forwards past scripted %s step", step.Type)
		}
		step = running.next()
	}
	if step.Type == StepCrash {
		return step.ExitCode
	}
	if step.Type == StepExit {
		return step.ExitCode
	}
	reply, busyMS, usage := running.turnReply(step)
	if !sleepOrCancel(proc.ctx, time.Duration(busyMS)*time.Millisecond) {
		warn(proc.stderr, "exec cancelled: %v", proc.ctx.Err())
		return ExitUsage
	}
	if len(step.Structured) > 0 {
		reply = string(step.Structured)
	}
	if !session.call.ephemeral {
		if err := session.recordAssistant(reply, usage); err != nil {
			warn(proc.stderr, "record exec reply: %v", err)
			return ExitUsage
		}
	}
	for _, event := range []map[string]any{
		{keyType: "item.completed", "item": map[string]any{keyType: "agent_message", "text": reply}},
		{keyType: "turn.completed", "usage": map[string]int64{codexInputTokens: usage.Input, codexOutputTokens: usage.Output, "cached_input_tokens": usage.CacheRead, "cache_creation_input_tokens": usage.CacheCreation}},
	} {
		if err := encoder.Encode(event); err != nil {
			warn(proc.stderr, "write exec result: %v", err)
			return ExitUsage
		}
	}
	return 0
}

func (session *codexSession) composerGlyph() string { return "›" }

// composerPlaceholder opts only Codex into internal/spawn/spawn.go's idle
// composer and rename-dialog protocol.
func (session *codexSession) composerPlaceholder() string { return "Ask Codex to do anything" }

const codexCompactMS = 5000

func (session *codexSession) compactBusyDuration(step Step) time.Duration {
	if step.BusyMS > 0 {
		return time.Duration(step.BusyMS) * time.Millisecond
	}
	return codexCompactMS * time.Millisecond
}

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
	codexInputTokens   = "input_tokens"
	codexOutputTokens  = "output_tokens"
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
	if session.rolloutFile == nil {
		return fmt.Errorf("rollout %s is not open", session.rollout)
	}
	if _, err := session.rolloutFile.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("append rollout %s: %w", session.rollout, err)
	}
	return nil
}

func (session *codexSession) openRollout() error {
	if err := os.MkdirAll(filepath.Dir(session.rollout), 0o700); err != nil {
		return fmt.Errorf("create rollout directory: %w", err)
	}
	file, err := os.OpenFile(session.rollout, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open rollout %s: %w", session.rollout, err)
	}
	session.rolloutFile = file
	return nil
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
	fresh := session.rollout == ""
	if fresh {
		session.rollout = session.rolloutFor(session.threadID, now)
	}
	if err := session.openRollout(); err != nil {
		return err
	}
	if fresh {
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
		session.proc.stderr,
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

func (session *codexSession) prompt(string) (string, error) { session.mcpError = ""; return "", nil }

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
				codexInputTokens:  usage.Input,
				codexOutputTokens: usage.Output,
				"total_tokens":    usage.Input + usage.Output,
			},
			"last_token_usage": map[string]any{
				codexInputTokens:  usage.Input,
				codexOutputTokens: usage.Output,
				"total_tokens":    usage.Input + usage.Output,
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
func (session *codexSession) compact(_ Step) error {
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
	if err := appendLine(filepath.Join(session.codexHome, codexmeta.SessionIndexFile), encoded); err != nil {
		return err
	}
	session.threadName = name
	return nil
}

// background spawns a subagent thread: its own rollout whose session_meta
// names this thread as parent (internal/codexmeta/header.go:71-84 reads it).
// Codex's pane shows nothing pfm pins for it, so no row is rendered.
func (session *codexSession) background(step Step) (string, error) {
	child := &codexSession{proc: session.proc, codexHome: session.codexHome, threadID: newUUID(), model: session.model}
	now := time.Now()
	child.rollout = child.rolloutFor(child.threadID, now)
	if err := child.openRollout(); err != nil {
		return "", err
	}
	defer func() {
		if err := child.finish("other"); err != nil {
			warn(session.proc.stderr, "finish subagent: %v", err)
		}
	}()
	if err := child.write(session.sessionMeta(child.threadID, session.threadID, now)); err != nil {
		return "", err
	}
	if err := child.recordUser("Subagent task for " + step.Name); err != nil {
		return "", err
	}
	return "", child.recordAssistant(step.Status, session.proc.script.Tokens)
}

// mcp spawns the command the [mcp_servers.<server>] table pfm wrote into
// config.toml names (`pfm mcp serve --stdio` in production), performs
// initialize + tools/list over its stdio, and records the tool names for the
// test to compare against the server's. Closing the session closes the
// child's stdin, which ends it.
func (session *codexSession) mcp(step Step) error {
	if step.Tool == "" {
		_, err := session.callMCP(step)
		return err
	}
	input := step.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	callID := "call_" + newUUID()[:8]
	if err := session.write(codexRecord{Type: recordResponseItem, Payload: map[string]any{
		keyType: "function_call", "name": step.Tool, "arguments": string(input), "call_id": callID,
	}}); err != nil {
		return err
	}
	answer, callErr := session.callMCP(step)
	if callErr != nil {
		answer = &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: callErr.Error()}}}
	}
	encoded, err := json.Marshal(answer)
	if err != nil {
		return fmt.Errorf("encode MCP %s result: %w", step.Tool, err)
	}
	if answer.IsError {
		session.mcpError = "MCP " + step.Tool + " failed: " + string(encoded)
		warn(session.proc.stderr, "%s", session.mcpError)
	}
	if err := session.proc.recorder.write("mcp-call-"+step.Tool+".json", string(encoded)+"\n"); err != nil {
		return err
	}
	return session.write(codexRecord{Type: recordResponseItem, Payload: map[string]any{
		keyType: "function_call_output", "call_id": callID, "output": string(encoded),
	}})
}

func (session *codexSession) callMCP(step Step) (*mcp.CallToolResult, error) {
	name := step.Server
	if name == "" {
		name = pfmconfig.MCPServerProfessor
	}
	var config struct {
		Servers map[string]struct {
			Command string   `toml:"command"`
			Args    []string `toml:"args"`
		} `toml:"mcp_servers"`
	}
	path := filepath.Join(session.codexHome, "config.toml")
	if _, err := toml.DecodeFile(path, &config); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	server, ok := config.Servers[name]
	if !ok || server.Command == "" {
		return nil, fmt.Errorf("%s has no [mcp_servers.%s] command", path, name)
	}
	bounded, cancel := mcpBoundedContext(session.proc.ctx)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "mock-engine", Version: session.proc.script.Version}, nil)
	launched := strings.Join(append([]string{server.Command}, server.Args...), " ")
	command := exec.CommandContext(bounded, server.Command, server.Args...)
	command.Dir = session.proc.cwd
	command.Env = append(
		append([]string(nil), session.environ...),
		"TMUX="+session.proc.env("TMUX"),
		"TMUX_PANE="+session.proc.env("TMUX_PANE"),
		"CODEX_HOME="+session.codexHome,
		"CODEX_THREAD_ID="+session.threadID,
	)
	connection, err := client.Connect(
		bounded,
		&mcp.CommandTransport{Command: command},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize against %s: %w", launched, err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			warn(session.proc.stderr, "close MCP session: %v", err)
		}
	}()
	tools, err := connection.ListTools(bounded, nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list against %s: %w", launched, err)
	}
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	encoded, err := json.Marshal(names)
	if err != nil {
		return nil, fmt.Errorf("encode tool names: %w", err)
	}
	if err := session.proc.recorder.write("mcp-"+name+".json", string(encoded)+"\n"); err != nil {
		return nil, err
	}
	if step.Tool == "" {
		return nil, nil
	}
	input := step.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	answer, err := connection.CallTool(bounded, &mcp.CallToolParams{Name: step.Tool, Arguments: input})
	if err != nil {
		return nil, fmt.Errorf("tools/call %s on MCP %s: %w", step.Tool, name, err)
	}
	return answer, nil
}

func (session *codexSession) clear() error {
	return errors.New("codex has no /clear in pfm's model of it (internal/reload drives resume, never clear)")
}

func (session *codexSession) finish(string) error {
	var closeErr, seatErr error
	if session.rolloutFile != nil {
		if err := session.rolloutFile.Close(); err != nil {
			closeErr = fmt.Errorf("close rollout %s: %w", session.rollout, err)
		}
		session.rolloutFile = nil
	}
	if session.seat != nil {
		seatErr = session.seat.release()
		session.seat = nil
	}
	return errors.Join(closeErr, seatErr)
}

// statusLine is internal/spawn/spawn.go's idle footer, keeping MCP failures
// on a separate line after the turn completes.
func (session *codexSession) statusLine(Tokens) (string, error) {
	label := session.threadName
	if label == "" {
		label = session.model
	}
	line := label + " · " + session.proc.cwd
	if session.mcpError != "" {
		line += "\r\n" + session.mcpError
	}
	return line, nil
}
