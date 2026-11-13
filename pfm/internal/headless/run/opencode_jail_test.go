//go:build jail

package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func opencodeValidEvents() string {
	return strings.Join([]string{
		`printf '%s\n' '{"type":"text","part":{"text":"answer","time":{"end":1}}}'`,
		`printf '%s\n' '{"type":"tool_use","part":{"tool":"StructuredOutput","state":{"status":"completed","input":{"answer":"answer"}}}}'`,
		`printf '%s\n' '{"type":"step_finish","part":{"reason":"stop","cost":0.125,"tokens":{"input":4,"output":2,"reasoning":1,"cache":{"read":3,"write":5}}}}'`,
	}, "\n")
}

// The child imports the exact generated plugin module through Node and runs it
// against host-shaped fixtures. This exercises the module itself; the separate
// missing-handshake test below keeps the process boundary fail-closed. The
// assertions are deliberately in the child because OpenCode's system transform
// contract requires mutating the array supplied by the host; replacing
// output.system can look equivalent in a shallow Go assertion while the host
// retains the original array.
func writeOpenCodeStub(t *testing.T, runBody string) string {
	t.Helper()
	return writeEngineStub(t, `if [ "${1:-}" = "serve" ]; then
  node --input-type=module - "$@" <<'NODE'
import fs from "node:fs"
import http from "node:http"

const args = process.argv.slice(2)
const portIndex = args.indexOf("--port")
const port = portIndex < 0 ? 0 : Number(args[portIndex + 1])
if (!Number.isInteger(port) || port < 0) throw new Error("fake OpenCode server did not receive a port")
let initialized = false
const assistantGets = new Map()
const server = http.createServer(async (request, response) => {
  const url = new URL(request.url ?? "/", "http://127.0.0.1")
  if (process.env.CAPTURE_DIR) fs.appendFileSync(process.env.CAPTURE_DIR + "/requests", request.method + " " + url.pathname + "\n")
  try {
    if (request.method === "GET" && url.pathname === "/global/health") {
      response.setHeader("content-type", "application/json")
      response.end(JSON.stringify({healthy: true}))
      return
    }
    if (request.method === "GET" && url.pathname === "/experimental/tool/ids") {
      if (process.env.CAPTURE_DIR) fs.appendFileSync(process.env.CAPTURE_DIR + "/catalog", "GET\n")
      if (!initialized && process.env.PFM_TEST_SERVER_LOAD_PLUGIN !== "0") {
        const config = JSON.parse(process.env.OPENCODE_CONFIG_CONTENT)
        const module = await import(config.plugin[0])
        const hooks = await module.default({})
        if (typeof hooks.config !== "function") throw new Error("missing config hook")
        hooks.config({mcp: {inherited: {type: "remote"}}})
        initialized = true
      }
      response.setHeader("content-type", "application/json")
      response.end(JSON.stringify(["bash", "read", "server_tool", "remote_extra"]))
      return
    }
    if (request.method === "POST" && url.pathname === "/session") {
      response.setHeader("content-type", "application/json")
      response.end(JSON.stringify({id: "seed"}))
      return
    }
    if (request.method === "POST" && url.pathname === "/session/seed/message") {
      let body = ""
      for await (const chunk of request) body += chunk
      const payload = JSON.parse(body)
      if (payload.format?.type === "json_schema" && process.env.PFM_TEST_SERVER_LOAD_PLUGIN !== "0") {
        const config = JSON.parse(process.env.OPENCODE_CONFIG_CONTENT)
        const module = await import(config.plugin[0])
        const hooks = await module.default({})
        const output = {message: {id: "seed-message", format: payload.format, tools: {}}}
        await hooks["chat.message"]({sessionID: "seed"}, output)
      }
      response.setHeader("content-type", "application/json")
      response.end(JSON.stringify({id: "seed-message"}))
      return
    }
    if (request.method === "DELETE" && url.pathname === "/session/seed") {
      response.statusCode = 204
      response.end()
      return
    }
    if (request.method === "GET" && url.pathname.startsWith("/session/") && url.pathname.endsWith("/message")) {
      // Persisted user messages can carry an Effect Schema class that loses
      // its prototype on hydration. The production collector must use the
      // assistant IDs recorded by the plugin event hook and avoid this bulk
      // endpoint entirely.
      response.statusCode = 400
      response.end("bulk history is not a valid result endpoint")
      return
    }
    if (request.method === "GET" && url.pathname.startsWith("/session/") && (url.pathname.endsWith("/message/assistant-1") || url.pathname.endsWith("/message/assistant-2"))) {
      const second = url.pathname.endsWith("/message/assistant-2")
      const two = process.env.PFM_TEST_TWO_ASSISTANTS === "1"
      const getCount = (assistantGets.get(url.pathname) ?? 0) + 1
      assistantGets.set(url.pathname, getCount)
      if (process.env.CAPTURE_DIR) fs.appendFileSync(process.env.CAPTURE_DIR + "/assistant-gets", url.pathname + "\n")
      const completed = !process.env.PFM_OPENCODE_SCHEMA_FILE || getCount > 1
      if (!completed) {
        response.setHeader("content-type", "application/json")
        response.end(JSON.stringify({info: {role: "assistant", parentID: "user", finish: "tool-calls", time: {}}, parts: [
          {type: "tool", tool: "StructuredOutput", state: {status: "completed", input: {answer: "answer"}}},
        ]}))
        return
      }
      response.setHeader("content-type", "application/json")
      if (two && !second) {
        response.end(JSON.stringify({info: {role: "assistant", parentID: "user", finish: "tool-calls", time: completed ? {completed: 1} : {}}, parts: [
          {type: "text", text: "intermediate", time: {end: 1}},
          {type: "tool", tool: "bash", state: {status: "completed", input: {command: "true"}}},
          {type: "step-finish", reason: "tool-calls", cost: 0.125, tokens: {input: 4, output: 2, reasoning: 1, cache: {read: 3, write: 5}}},
        ]}))
      } else if (two && second) {
        response.end(JSON.stringify({info: {role: "assistant", parentID: "user", finish: "stop", structured: {answer: "second"}, time: completed ? {completed: 2} : {}}, parts: [
          {type: "text", text: "final", time: {end: 2}},
          {type: "tool", tool: "StructuredOutput", state: {status: "completed", input: {answer: "second"}}},
          {type: "step-finish", reason: "stop", cost: 0.25, tokens: {input: 6, output: 3, reasoning: 2, cache: {read: 7, write: 9}}},
        ]}))
      } else {
        response.end(JSON.stringify({info: {role: "assistant", parentID: "user", finish: process.env.PFM_OPENCODE_SCHEMA_FILE ? "tool-calls" : "stop", structured: {answer: "answer"}, time: completed ? {completed: 1} : {}}, parts: [
          {type: "text", text: "answer", time: {end: 1}},
          {type: "tool", tool: "StructuredOutput", state: {status: "completed", input: {answer: "answer"}}},
          {type: "step-finish", reason: "stop", cost: 0.125, tokens: {input: 4, output: 2, reasoning: 1, cache: {read: 3, write: 5}}},
        ]}))
      }
      return
    }
    response.statusCode = 404
    response.end("not found")
  } catch (error) {
    console.error(String(error))
    response.statusCode = 500
    response.end("plugin initialization failed")
  }
})
server.listen(port, "127.0.0.1", () => console.log("opencode server listening on http://127.0.0.1:" + server.address().port))
NODE
  exit 0
fi
if [ "${1:-}" = "run" ]; then
`+runBody+`
  if [ "${PFM_TEST_SERVER_LOAD_PLUGIN:-1}" != "0" ] && [ -n "${PFM_OPENCODE_ASSISTANTS_FILE:-}" ]; then
    emit_assistant() {
    node --input-type=module <<'NODE'
import fs from "node:fs"
const config = JSON.parse(process.env.OPENCODE_CONFIG_CONTENT)
const module = await import(config.plugin[0])
const hooks = await module.default({})
if (typeof hooks.event !== "function") throw new Error("missing event hook")
if (typeof hooks["chat.message"] !== "function") throw new Error("missing chat.message hook")
if (process.env.PFM_OPENCODE_SCHEMA_FILE) await hooks["chat.message"]({sessionID: "seed"}, {message: {id: "seed", format: {type: "json_schema", schema: {type: "object"}, retryCount: 2}, tools: {}}})
await hooks["chat.message"]({sessionID: "run"}, {message: {id: "user", format: {type: "text"}, tools: {}}})
await hooks.event({event: {type: "message.updated", properties: {
  info: {sessionID: "run", role: "assistant", parentID: "user", id: "assistant-1"}
}}})
if (process.env.PFM_TEST_TWO_ASSISTANTS === "1") await hooks.event({event: {type: "message.updated", properties: {
  info: {sessionID: "run", role: "assistant", parentID: "user", id: "assistant-2"}
}}})
if (process.env.CAPTURE_DIR) fs.writeFileSync(process.env.CAPTURE_DIR + "/native-complete", "done\n")
NODE
    }
	if [ "${PFM_TEST_DEFER_ASSISTANT:-0}" = "1" ]; then
      (sleep 0.1; emit_assistant) > /dev/null 2>&1 &
    else
      emit_assistant
    fi
  fi
  if [ -n "${PFM_OPENCODE_MESSAGE_FILE:-}" ] && [ ! -f "$PFM_OPENCODE_MESSAGE_FILE" ]; then
    printf '%s\n' '{"sessionID":"run","messageID":"user"}' > "$PFM_OPENCODE_MESSAGE_FILE"
  fi
  exit 0
fi
echo "unexpected fake OpenCode invocation: $*" >&2
exit 2`)
}

func TestRunOpenCodeFailsClosedWhenPluginDoesNotLoad(t *testing.T) {
	headlessJail(t)
	binary := writeOpenCodeStub(t, opencodeValidEvents())
	result, err := Run(context.Background(), Request{
		Config: opencodeMachine(binary, opencodeHome(t)), Engine: pfmengine.OpenCode,
		TempDir: t.TempDir(), Prompt: "hello", Timeout: 5 * time.Second,
		Env: testEnv(t.TempDir(), "PFM_TEST_SERVER_LOAD_PLUGIN=0"),
	})
	if err == nil || !strings.Contains(err.Error(), "plugin") {
		t.Fatalf("Run() error = %v, want fail-closed plugin-load error", err)
	}
	if !result.IsError {
		t.Fatal("missing OpenCode plugin handshake returned a successful result")
	}
}

func TestRunOpenCodeNativeUsesNodePluginAndPreservesOutput(t *testing.T) {
	headlessJail(t)
	binary := writeOpenCodeStub(t, `node --input-type=module <<'NODE'
const config = JSON.parse(process.env.OPENCODE_CONFIG_CONTENT)
const module = await import(config.plugin[0])
const hooks = await module.default({})
hooks.config({})
NODE
printf 'native OpenCode stream\n'`)
	var stdout bytes.Buffer
	result, err := Run(context.Background(), Request{
		Config: opencodeMachine(binary, opencodeHome(t)), Engine: pfmengine.OpenCode,
		TempDir: t.TempDir(), Prompt: "hello", Native: true, Stdout: &stdout, Env: testEnv(t.TempDir()),
	})
	if err != nil {
		t.Fatalf("native Run() error = %v", err)
	}
	if result.Answer != "native OpenCode stream\n" || stdout.String() != result.Answer {
		t.Fatalf("native answer/stdout = %q/%q", result.Answer, stdout.String())
	}
}

func TestRunOpenCodeNativeWaitsForPersistedCompletion(t *testing.T) {
	headlessJail(t)
	capture := t.TempDir()
	binary := writeOpenCodeStub(t, `printf 'native OpenCode stream\n'`)
	var stdout bytes.Buffer
	result, err := Run(context.Background(), Request{
		Config: opencodeMachine(binary, opencodeHome(t)), Engine: pfmengine.OpenCode,
		TempDir: t.TempDir(), Prompt: "hello", Native: true, Timeout: 10 * time.Second, Stdout: &stdout,
		Env: testEnv(capture, "CAPTURE_DIR="+capture, "PFM_TEST_DEFER_ASSISTANT=1"),
	})
	if err != nil {
		t.Fatalf("native OpenCode Run() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(capture, "native-complete")); err != nil {
		t.Fatalf("native OpenCode returned before persisted assistant completion: %v", err)
	}
	if result.Answer != "native OpenCode stream\n" || stdout.String() != result.Answer {
		t.Fatalf("native answer/stdout = %q/%q", result.Answer, stdout.String())
	}
}

func TestRunOpenCodeTimeoutKillsProcessGroup(t *testing.T) {
	headlessJail(t)
	capture := t.TempDir()
	scratch := t.TempDir()
	binary := writeOpenCodeStub(t, `(sleep 30) &
echo "$!" > "$CAPTURE_DIR/child-pid"
sleep 30`)
	ctx := newArmedDeadlineContext()
	done := make(chan struct{})
	var result Result
	var runErr error
	go func() {
		result, runErr = Run(ctx, Request{
			Config: opencodeMachine(binary, opencodeHome(t)), Engine: pfmengine.OpenCode,
			TempDir: scratch, Prompt: "hello", Env: testEnv(capture),
		})
		close(done)
	}()
	waitForFile(t, filepath.Join(capture, "child-pid"), 3*time.Second)
	ctx.arm()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("OpenCode Run() did not return after the deadline was armed")
	}
	if runErr == nil || !errors.Is(runErr, context.DeadlineExceeded) || !result.TimedOut {
		t.Fatalf("timeout err/result = %v/%#v", runErr, result)
	}
	var childPID int
	if _, err := fmt.Sscanf(
		strings.TrimSpace(string(mustRead(t, filepath.Join(capture, "child-pid")))),
		"%d",
		&childPID,
	); err != nil {
		t.Fatal(err)
	}
	waitForProcessExit(t, childPID)
}

func TestRunOpenCodePersistenceUsesConfiguredDataHome(t *testing.T) {
	headlessJail(t)
	capture := t.TempDir()
	binary := writeOpenCodeStub(t, `printf '%s\n' "$XDG_DATA_HOME" > "$CAPTURE_DIR/data-home"
printf '%s\n' 'pfm-opencode-plugin-ready' > "$PFM_OPENCODE_PLUGIN_READY"
`+opencodeValidEvents())
	accountHome := opencodeHome(t)
	accountDataHome := filepath.Dir(accountHome)
	result, err := Run(context.Background(), Request{
		Config: opencodeMachine(binary, accountHome), Engine: pfmengine.OpenCode,
		TempDir: t.TempDir(), Prompt: "hello", Env: testEnv(capture, "CAPTURE_DIR="+capture),
	})
	if err != nil {
		t.Fatalf("persistent OpenCode Run() error = %v", err)
	}
	if result.Answer != "answer" {
		t.Fatalf("answer = %q, want answer", result.Answer)
	}
	if got := strings.TrimSpace(string(mustRead(t, filepath.Join(capture, "data-home")))); got != accountDataHome {
		t.Fatalf("persistent XDG_DATA_HOME = %q, want configured data home %q", got, accountDataHome)
	}
}

func TestRunOpenCodeSealedOwnsScratchAndClearsInheritedControls(t *testing.T) {
	headlessJail(t)
	base := t.TempDir()
	capture := t.TempDir()
	requestedCWD := t.TempDir()
	accountHome := opencodeHome(t)
	binary := writeOpenCodeStub(t, `pwd > "$CAPTURE_DIR/pwd"
printf '%s\n' "$XDG_DATA_HOME" > "$CAPTURE_DIR/data-home"
printf '%s\n' "$XDG_CONFIG_HOME" > "$CAPTURE_DIR/config-home"
printf '%s\n' "$OPENCODE_DISABLE_PROJECT_CONFIG" > "$CAPTURE_DIR/disable-project"
printf '%s\n' "$PFM_OPENCODE_STRICT_MCP" > "$CAPTURE_DIR/strict-mcp"
printf '%s\n' 'pfm-opencode-plugin-ready' > "$PFM_OPENCODE_PLUGIN_READY"
`+opencodeValidEvents())
	system := "sealed replacement"
	result, err := Run(context.Background(), Request{
		Config: opencodeMachine(binary, accountHome), Engine: pfmengine.OpenCode,
		Prompt: "hello", CWD: requestedCWD, TempDir: base, SystemPrompt: &system,
		Sealed: true, Env: testEnv(capture, "CAPTURE_DIR="+capture,
			"OPENCODE_CONFIG=ambient-config", "OPENCODE_CONFIG_DIR=ambient-dir"),
	})
	if err != nil {
		t.Fatalf("sealed OpenCode Run() error = %v", err)
	}
	if result.Answer != "answer" {
		t.Fatalf("answer = %q, want answer", result.Answer)
	}
	pwd := strings.TrimSpace(string(mustRead(t, filepath.Join(capture, "pwd"))))
	if filepath.Clean(pwd) == filepath.Clean(requestedCWD) || !insideTempBase(t, pwd, base) {
		t.Fatalf("sealed cwd = %q, want a temporary child of %q", pwd, base)
	}
	if _, err := os.Stat(pwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sealed cwd was not removed: stat err=%v", err)
	}
	if got := strings.TrimSpace(
		string(mustRead(t, filepath.Join(capture, "data-home"))),
	); got == filepath.Dir(
		accountHome,
	) {
		t.Fatalf("sealed XDG_DATA_HOME reused configured data home %q", got)
	}
	if got := strings.TrimSpace(
		string(mustRead(t, filepath.Join(capture, "config-home"))),
	); got == "" ||
		got == "ambient-config" {
		t.Fatalf("sealed XDG_CONFIG_HOME = %q, want private config home", got)
	}
	if got := strings.TrimSpace(string(mustRead(t, filepath.Join(capture, "disable-project")))); got != "1" {
		t.Fatalf("sealed project config control = %q, want 1", got)
	}
	if got := strings.TrimSpace(string(mustRead(t, filepath.Join(capture, "strict-mcp")))); got != "1" {
		t.Fatalf("sealed strict MCP control = %q, want 1", got)
	}
}

func TestRunOpenCodeCollectsAllAssistantIDsAndAggregatesUsage(t *testing.T) {
	headlessJail(t)
	capture := t.TempDir()
	binary := writeOpenCodeStub(t, opencodeValidEvents())
	schema := json.RawMessage(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`)
	result, err := Run(context.Background(), Request{
		Config:  opencodeMachine(binary, opencodeHome(t)),
		Engine:  pfmengine.OpenCode,
		TempDir: t.TempDir(),
		Prompt:  "hello",
		Schema:  schema,
		Env:     testEnv(capture, "CAPTURE_DIR="+capture, "PFM_TEST_TWO_ASSISTANTS=1"),
	})
	if err != nil {
		t.Fatalf("OpenCode multi-assistant Run() error = %v", err)
	}
	if result.Answer != `{"answer":"second"}` {
		t.Fatalf("answer = %q, want final structured answer", result.Answer)
	}
	if result.Usage == nil || result.Usage.Input != 10 || result.Usage.Output != 8 || result.Usage.CachedInput != 10 ||
		result.Usage.CacheCreation != 14 {
		t.Fatalf("aggregated usage = %#v, want input=10 output=8 cached=10 cache_creation=14", result.Usage)
	}
	if result.TotalCostUSD == nil || *result.TotalCostUSD != 0.375 {
		t.Fatalf("aggregated cost = %#v, want 0.375", result.TotalCostUSD)
	}
	if gets := capturedLines(t, filepath.Join(capture, "assistant-gets")); len(gets) < 4 {
		t.Fatalf(
			"schema collector did not wait for complete snapshots for both assistant IDs: %d GET(s), want at least 4",
			len(gets),
		)
	}
}
