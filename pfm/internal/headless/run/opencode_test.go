package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func opencodeMachine(binary, home string) pfmconfig.Config {
	return pfmconfig.Config{
		OpenCode:         pfmconfig.OpenCodePrefs{Binary: binary},
		OpenCodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 7, Home: home}},
	}
}

func opencodeHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "opencode")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

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

func TestArgumentsOpenCodeMapsModelEffortFormatAndNativeArgs(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		request Request
		want    []string
	}{
		{
			name:    "normalized gpt model",
			request: Request{Engine: pfmengine.OpenCode, Model: "gpt-5.6-luna", Effort: "high", Args: []string{"--future", "value"}},
			want:    []string{"run", "--model", "openai/gpt-5.6-luna", "--variant", "high", "--format", "json", "--future", "value"},
		},
		{
			name:    "provider-qualified native model",
			request: Request{Engine: pfmengine.OpenCode, Model: "openai/gpt-5.6-luna", Native: true},
			want:    []string{"run", "--model", "openai/gpt-5.6-luna"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := arguments(testCase.request)
			if err != nil {
				t.Fatalf("arguments() error = %v", err)
			}
			if !equalStrings(got, testCase.want) {
				t.Fatalf("arguments() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestValidateOpenCodeArgsRejectsOnlyConflictingControls(t *testing.T) {
	base := Request{Engine: pfmengine.OpenCode, Model: "model", Effort: "medium"}
	cases := []struct {
		name    string
		request Request
		arg     string
		wantErr bool
	}{
		{name: "model", request: base, arg: "--model=other", wantErr: true},
		{name: "variant", request: base, arg: "--variant", wantErr: true},
		{name: "pure", request: base, arg: "--pure", wantErr: true},
		{name: "format normalized", request: base, arg: "--format", wantErr: true},
		{
			name:    "format native",
			request: Request{Engine: pfmengine.OpenCode, Native: true},
			arg:     "--format",
			wantErr: false,
		},
		{name: "dir without cwd", request: base, arg: "--dir", wantErr: false},
		{
			name:    "dir with cwd",
			request: Request{Engine: pfmengine.OpenCode, CWD: t.TempDir()},
			arg:     "--dir",
			wantErr: true,
		},
		{name: "agent without replacement", request: base, arg: "--agent", wantErr: false},
		{
			name:    "agent with replacement",
			request: Request{Engine: pfmengine.OpenCode, SystemPrompt: stringPtr("replacement")},
			arg:     "--agent",
			wantErr: true,
		},
		{
			name:    "session without persistence",
			request: Request{Engine: pfmengine.OpenCode, NoSessionPersistence: true},
			arg:     "--session",
			wantErr: true,
		},
		{name: "session with persistence", request: base, arg: "--session", wantErr: false},
		{
			name:    "sealed native arg",
			request: Request{Engine: pfmengine.OpenCode, Sealed: true},
			arg:     "--future-flag",
			wantErr: true,
		},
		{name: "future flag", request: base, arg: "--future-flag", wantErr: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateOpenCodeArgs([]string{testCase.arg}, testCase.request)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("validateOpenCodeArgs(%q) error = %v, wantErr=%v", testCase.arg, err, testCase.wantErr)
			}
		})
	}
}

func TestResolveOpenCodeUsesConfiguredBinaryAccountAndPrefs(t *testing.T) {
	headlessJail(t)
	binary := writeEngineStub(t, "printf '%s\\n' 'unused'")
	accountHome := opencodeHome(t)
	config := opencodeMachine(binary, accountHome)
	config.Ask.Prefs = map[pfmengine.ID]pfmconfig.EnginePrefs{
		pfmengine.OpenCode: {Model: "gpt-5.6-luna", Effort: "high"},
	}
	resolved, err := Resolve(Request{
		Config: config, Engine: pfmengine.OpenCode, ConfigDir: accountHome,
		Native: true,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Account != 7 || resolved.ConfigDir != accountHome {
		t.Fatalf("resolved account/config dir = %d/%q, want 7/%q", resolved.Account, resolved.ConfigDir, accountHome)
	}
	if resolved.Model != "gpt-5.6-luna" || resolved.Effort != "high" {
		t.Fatalf("native OpenCode prefs = %q/%q, want gpt-5.6-luna/high", resolved.Model, resolved.Effort)
	}
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

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
