package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"uuid"

	"github.com/BurntSushi/toml"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// codexSession is the Codex engine behind the shared pane loop: one thread's
// rollout, its session_index rows, hooks.json and the MCP client.
type codexSession struct {
	proc          *process
	call          codexInvocation
	codexHome     string
	threadID      string
	rollout       string
	rolloutFile   *os.File
	model         string
	threadName    string
	hooks         hookSet
	environ       []string
	seat          *seat
	mcpError      string
	turnID        string
	lastAssistant string
	started       bool
	ended         bool
	attention     string
	pendingTool   map[string]any
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
		session.threadID = uuid.NewV4().String()
	}
	if call.subcommand == "exec" {
		return session.headless()
	}
	return runPane(proc, session, call.prompt, call.resumed)
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
	codexCallIDKey     = "call_id"
	codexModelKey      = "model"
	codexInfoKey       = "info"
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
// internal/transcript.FlattenPromptText and internal/transcript.Visible read.
type codexMessage struct {
	Type    string         `json:"type"`
	Role    string         `json:"role"`
	Content []codexContent `json:"content"`
}

type codexContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
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
	callID := "call_" + uuid.NewV4().String()[:8]
	if err := session.write(codexRecord{Type: recordResponseItem, Payload: map[string]any{
		keyType: "function_call", openCodeNameKey: step.Tool, "arguments": string(input), codexCallIDKey: callID,
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
		keyType: "function_call_output", codexCallIDKey: callID, "output": string(encoded),
	}})
}

func (session *codexSession) callMCP(step Step) (*mcp.CallToolResult, error) {
	name := mcpServerName(step)
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
	environment := append(
		session.proc.terminalEnvironment(session.environ),
		"CODEX_HOME="+session.codexHome,
		"CODEX_THREAD_ID="+session.threadID,
	)
	return session.proc.callLocalMCP(
		step,
		append([]string{server.Command}, server.Args...),
		environment,
		localMCPOptions{ListTools: true},
	)
}

func (session *codexSession) clear() error {
	return errors.New("codex has no /clear in pfm's model of it (internal/reload drives resume, never clear)")
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
