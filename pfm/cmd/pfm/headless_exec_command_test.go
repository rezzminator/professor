package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func headlessCLIJail(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "tmux"))
	t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))
	t.Setenv("PFM_HOME", filepath.Join(root, "home"))
}

func writeHeadlessCLIStub(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engine")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeHeadlessOpenCodeStub is the fake `opencode` binary the CLI tests drive:
// `serve` is a Node loopback server answering the real endpoints pfm calls,
// and `run` is the attached invocation. No real OpenCode binary, no network,
// no model — the same seam every other engine stub in this file uses.
func writeHeadlessOpenCodeStub(t *testing.T, runBody string) string {
	t.Helper()
	return writeHeadlessCLIStub(t, `if [ "${1:-}" = "serve" ]; then
  node --input-type=module - "$@" <<'NODE'
import fs from "node:fs"
import http from "node:http"

const args = process.argv.slice(2)
const portIndex = args.indexOf("--port")
const port = portIndex < 0 ? 0 : Number(args[portIndex + 1])
if (!Number.isInteger(port) || port < 0) throw new Error("fake OpenCode server did not receive a port")
const server = http.createServer((request, response) => {
  const url = new URL(request.url ?? "/", "http://127.0.0.1")
  if (request.method === "GET" && url.pathname === "/global/health") {
    response.setHeader("content-type", "application/json")
    response.end(JSON.stringify({healthy: true}))
    return
  }
  if (request.method === "GET" && url.pathname === "/experimental/tool/ids") {
    fs.writeFileSync(process.env.PFM_OPENCODE_PLUGIN_READY, "pfm-opencode-plugin-ready\n")
    response.setHeader("content-type", "application/json")
    response.end("[]")
    return
  }
  if (request.method === "POST" && url.pathname === "/session") {
    response.setHeader("content-type", "application/json")
    response.end(JSON.stringify({id: "seed"}))
    return
  }
  if (request.method === "POST" && url.pathname === "/session/seed/message") {
    let body = ""
    request.on("data", (chunk) => body += chunk)
    request.on("end", () => {
      try {
        const payload = JSON.parse(body)
        if (payload.format?.type === "json_schema" && process.env.PFM_OPENCODE_SCHEMA_READY) fs.writeFileSync(process.env.PFM_OPENCODE_SCHEMA_READY, "pfm-opencode-schema-ready\n")
        response.setHeader("content-type", "application/json")
        response.end(JSON.stringify({id: "seed-message"}))
      } catch (error) {
        response.statusCode = 400
        response.end(String(error))
      }
    })
    return
  }
  if (request.method === "DELETE" && url.pathname === "/session/seed") {
    response.statusCode = 204
    response.end()
    return
  }
  if (request.method === "GET" && url.pathname.endsWith("/message/assistant-1")) {
    const schema = Boolean(process.env.PFM_OPENCODE_SCHEMA_FILE)
    const message = schema ? {
      info: {role: "assistant", parentID: "user", finish: "stop", structured: {ok: true}},
      parts: [{type: "tool", tool: "StructuredOutput", state: {status: "completed", input: {ok: true}}}, {type: "step-finish", reason: "stop"}],
    } : {
      info: {role: "assistant", parentID: "user", finish: "stop"},
      parts: [{type: "text", text: "opencode answer", time: {end: 1}}, {type: "step-finish", reason: "stop"}],
    }
    response.setHeader("content-type", "application/json")
    response.end(JSON.stringify(message))
    return
  }
  response.statusCode = 404
  response.end("not found")
})
server.listen(port, "127.0.0.1", () => console.log("opencode server listening on http://127.0.0.1:" + server.address().port))
NODE
  exit 0
fi
if [ "${1:-}" = "run" ]; then
`+runBody+`
  if [ -n "${PFM_OPENCODE_ASSISTANTS_FILE:-}" ]; then
    printf '%s\n' '{"sessionID":"run","userMessageID":"user","assistantIDs":["assistant-1"]}' > "$PFM_OPENCODE_ASSISTANTS_FILE"
  fi
  exit 0
fi
echo "unexpected fake OpenCode invocation: $*" >&2
exit 2`)
}

func headlessCLIRuntime(t *testing.T, binary string) commandRuntime {
	return headlessCLIRuntimeFor(t, binary, pfmengine.Claude)
}

func headlessCLIRuntimeFor(t *testing.T, binary string, engine pfmengine.ID) commandRuntime {
	t.Helper()
	configDir := filepath.Join(t.TempDir(), "engine-home")
	config := pfmconfig.Config{}
	if engine == pfmengine.Codex {
		config.Codex = pfmconfig.CodexPrefs{Binary: binary}
		config.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: configDir}}
	} else if engine == pfmengine.OpenCode {
		configDir = filepath.Join(t.TempDir(), "opencode")
		config.OpenCode = pfmconfig.OpenCodePrefs{Binary: binary}
		config.OpenCodeAccounts = []pfmconfig.OpenCodeAccount{{ID: 1, Home: configDir}}
	} else {
		config.Claude = pfmconfig.ClaudePrefs{Binary: binary}
		config.Accounts = []pfmconfig.Account{{ID: 1, ConfigDir: configDir}}
	}
	return commandRuntime{
		Config: config,
		Paths:  paths.Values{SIDDir: filepath.Join(t.TempDir(), "sid")},
	}
}

func TestHeadlessExecNormalizedJSONAndReceiptPreserveNullCost(t *testing.T) {
	headlessCLIJail(t)
	binary := writeHeadlessCLIStub(
		t,
		`printf '%s\n' '{"result":"hello","usage":{"input_tokens":2,"output_tokens":1},"total_cost_usd":null}'`,
	)
	commandEnv := headlessCLIRuntime(t, binary)
	receipt := filepath.Join(t.TempDir(), "receipt.jsonl")
	var stdout, stderr bytes.Buffer
	code := runHeadlessExec([]string{
		"--engine", "claude", "--prompt", "hello", "--output-format", "json", "--receipt", receipt,
	}, strings.NewReader("unused"), &stdout, &stderr, commandEnv)
	if code != 0 {
		t.Fatalf("exit = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("result is not JSON: %v (%q)", err, stdout.String())
	}
	if result["result"] != "hello" || result["is_error"] != false {
		t.Fatalf("result = %#v", result)
	}
	if value, ok := result["total_cost_usd"]; !ok || value != nil {
		t.Fatalf("JSON cost = %#v, want explicit null", value)
	}
	receiptBody, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var receiptValue map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(receiptBody), &receiptValue); err != nil {
		t.Fatalf("receipt is not JSON: %v", err)
	}
	if value, ok := receiptValue["cost_usd"]; !ok || value != nil {
		t.Fatalf("receipt cost = %#v, want explicit null", value)
	}
}

func TestHeadlessExecNativeStreamsStdinAndTailArgs(t *testing.T) {
	headlessCLIJail(t)
	binary := writeHeadlessCLIStub(t, `cat`)
	commandEnv := headlessCLIRuntime(t, binary)
	var stdout, stderr bytes.Buffer
	code := runHeadlessExec([]string{
		"--engine", "claude", "--output-format", "native", "--timeout", "2", "--", "--future-flag", "value with spaces",
	}, strings.NewReader("stream me\n"), &stdout, &stderr, commandEnv)
	if code != 0 || stdout.String() != "stream me\n" || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestHeadlessExecRejectsInvalidSchemaBeforeLaunch(t *testing.T) {
	headlessCLIJail(t)
	marker := filepath.Join(t.TempDir(), "started")
	binary := writeHeadlessCLIStub(t, `printf started > "`+marker+`"`)
	commandEnv := headlessCLIRuntime(t, binary)
	var stdout, stderr bytes.Buffer
	code := runHeadlessExec([]string{
		"--engine", "claude", "--prompt", "hello", "--json-schema", "not-json",
	}, strings.NewReader(""), &stdout, &stderr, commandEnv)
	if code != 2 || !strings.Contains(stderr.String(), "schema is not valid JSON") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("invalid schema launched engine; stat err=%v", err)
	}
}

func TestHeadlessExecHelpNamesSharedInterface(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runHeadlessExec(
		[]string{"--help"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		commandRuntime{},
	); code != 0 {
		t.Fatalf("help exit = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, phrase := range []string{
		"--engine claude|codex|opencode", "--system TEXT | --system-file FILE", "--sealed",
		"--allow-unsupported", "--env KEY=VALUE", "--engine-arg ARG", "--output-format text|json|native",
	} {
		if !strings.Contains(stderr.String(), phrase) {
			t.Fatalf("help omitted %q: %q", phrase, stderr.String())
		}
	}
}

func TestHeadlessConsumersUseSharedRunner(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(current)
	cases := []struct {
		path       string
		mustHave   []string
		mustAbsent []string
	}{
		{
			filepath.Join(root, "..", "..", "internal", "ask", "ask.go"),
			[]string{"headlessrun.Run("},
			[]string{"exec.Command", "exec.CommandContext"},
		},
		{
			filepath.Join(root, "..", "..", "internal", "stats", "limits.go"),
			[]string{"headlessrun.Run("},
			[]string{"exec.Command", "exec.CommandContext"},
		},
		{
			filepath.Join(root, "..", "..", "internal", "doctor", "harness_prompt.go"),
			[]string{"headlessrun.Run("},
			nil,
		},
	}
	for _, testCase := range cases {
		body, err := os.ReadFile(testCase.path)
		if err != nil {
			t.Fatalf("read %s: %v", testCase.path, err)
		}
		source := string(body)
		for _, want := range testCase.mustHave {
			if !strings.Contains(source, want) {
				t.Errorf("%s does not name required shared boundary %q", testCase.path, want)
			}
		}
		for _, forbidden := range testCase.mustAbsent {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s still contains direct harness subprocess call %q", testCase.path, forbidden)
			}
		}
	}
}

func TestHeadlessExecLabInvocationBothEngines(t *testing.T) {
	headlessCLIJail(t)
	for _, engine := range []struct {
		name string
		id   pfmengine.ID
	}{
		{name: "claude", id: pfmengine.Claude},
		{name: "opencode", id: pfmengine.OpenCode},
	} {
		t.Run(engine.name, func(t *testing.T) {
			capture := t.TempDir()
			var binary string
			if engine.id == pfmengine.OpenCode {
				binary = writeHeadlessOpenCodeStub(t, "cat > \"$CAPTURE_DIR/prompt\"\n"+
					"printf '%s\n' \"$@\" > \"$CAPTURE_DIR/args\"")
			} else {
				binary = writeHeadlessCLIStub(t, strings.Join([]string{
					"cat > \"$CAPTURE_DIR/prompt\"",
					"printf '%s\n' \"$@\" > \"$CAPTURE_DIR/args\"",
					"printf '%s\n' '{\"result\":\"ok\",\"structured_output\":{\"ok\":true},\"total_cost_usd\":null}'",
				}, "\n"))
			}
			commandEnv := headlessCLIRuntimeFor(t, binary, engine.id)
			first := filepath.Join(t.TempDir(), "prompt.md")
			second := filepath.Join(t.TempDir(), "input.md")
			system := filepath.Join(t.TempDir(), "system.md")
			schema := filepath.Join(t.TempDir(), "schema.json")
			out := filepath.Join(t.TempDir(), "out.json")
			receipt := filepath.Join(t.TempDir(), "receipt.jsonl")
			if err := os.WriteFile(first, []byte("first source"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(second, []byte("second source"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(system, []byte("system replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				schema,
				[]byte("{\"type\":\"object\",\"required\":[\"ok\"],\"properties\":{\"ok\":{\"type\":\"boolean\"}}}"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			args := []string{
				"--engine", engine.name,
				"--files", first, second,
				"--labels", "prompt", "input",
				"--task", "judge these",
				"--schema", schema,
				"--output-format", "json",
				"--out", out,
				"--model", "gpt-model-x",
				"--effort", "high",
				"--timeout", "20",
				"--config-dir", cliConfigDir(commandEnv, engine.id),
				"--system-file", system,
				"--receipt", receipt,
				"--env", "CAPTURE_DIR=" + capture,
			}
			if engine.id == pfmengine.Claude {
				args = append(args, "--sealed")
			}
			var stdout, stderr bytes.Buffer
			code := runHeadlessExec(args, strings.NewReader("ignored"), &stdout, &stderr, commandEnv)
			if code != 0 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			wantPrompt := "===== FILE 1: prompt =====\nfirst source\n===== END FILE 1 =====\n\n" +
				"===== FILE 2: input =====\nsecond source\n===== END FILE 2 =====\n\n" +
				"TASK: judge these\n"
			if got := string(mustReadCLI(t, filepath.Join(capture, "prompt"))); got != wantPrompt {
				t.Fatalf("prompt = %q, want %q", got, wantPrompt)
			}
			var normalized map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &normalized); err != nil {
				t.Fatalf("normalized output is not JSON: %v (%q)", err, stdout.String())
			}
			if got := normalized["structured_output"]; got == nil {
				t.Fatalf("normalized output omitted structured_output: %#v", normalized)
			}
			if body := strings.TrimSpace(string(mustReadCLI(t, out))); body != "{\"ok\":true}" {
				t.Fatalf("out = %q, want normalized schema output", body)
			}
			var receiptValue map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(mustReadCLI(t, receipt)), &receiptValue); err != nil {
				t.Fatalf("receipt is not JSON: %v", err)
			}
			if value, ok := receiptValue["cost_usd"]; !ok || value != nil {
				t.Fatalf("receipt cost = %#v, want explicit null", value)
			}
			argsSeen := cliLines(t, filepath.Join(capture, "args"))
			if engine.id == pfmengine.Claude {
				if !cliHas(argsSeen, "--effort") || !cliHas(argsSeen, "high") {
					t.Fatalf("Claude effort missing from engine argv: %#v", argsSeen)
				}
				for _, want := range []string{"--safe-mode", "--tools", "--setting-sources", "--strict-mcp-config", "--no-session-persistence"} {
					if !cliHas(argsSeen, want) {
						t.Fatalf("sealed Claude argv missing %q: %#v", want, argsSeen)
					}
				}
			} else if !cliHas(argsSeen, "--variant") || !cliHas(argsSeen, "high") {
				t.Fatalf("OpenCode effort mapping missing: %#v", argsSeen)
			}
		})
	}
}

func TestHeadlessExecTaskFileTrimsAndPromptStaysRaw(t *testing.T) {
	headlessCLIJail(t)
	capture := t.TempDir()
	binary := writeHeadlessCLIStub(
		t,
		"cat > \"$CAPTURE_DIR/prompt\"\nprintf '%s\\n' '{\"result\":\"ok\",\"total_cost_usd\":null}'",
	)
	commandEnv := headlessCLIRuntime(t, binary)
	source := filepath.Join(t.TempDir(), "source.md")
	task := filepath.Join(t.TempDir(), "task.txt")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(task, []byte("  task from file  \n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runHeadlessExec([]string{
		"--engine", "claude", "--files", source, "--task-file", task,
		"--env", "CAPTURE_DIR=" + capture,
	}, strings.NewReader("ignored"), &stdout, &stderr, commandEnv)
	if code != 0 {
		t.Fatalf("task-file exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	wantFramed := "===== FILE 1: source.md =====\nsource\n===== END FILE 1 =====\n\nTASK: task from file\n"
	if got := string(mustReadCLI(t, filepath.Join(capture, "prompt"))); got != wantFramed {
		t.Fatalf("task-file prompt = %q, want %q", got, wantFramed)
	}

	rawCapture := t.TempDir()
	rawBinary := writeHeadlessCLIStub(
		t,
		"cat > \"$CAPTURE_DIR/prompt\"\nprintf '%s\\n' '{\"result\":\"ok\",\"total_cost_usd\":null}'",
	)
	rawRuntime := headlessCLIRuntime(t, rawBinary)
	stdout.Reset()
	stderr.Reset()
	code = runHeadlessExec([]string{
		"--engine", "claude", "--prompt", "raw prompt",
		"--env", "CAPTURE_DIR=" + rawCapture,
	}, strings.NewReader("ignored"), &stdout, &stderr, rawRuntime)
	if code != 0 {
		t.Fatalf("raw prompt exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := string(mustReadCLI(t, filepath.Join(rawCapture, "prompt"))); got != "raw prompt" {
		t.Fatalf("raw prompt was framed or changed: %q", got)
	}
}

func TestHeadlessExecFilesValidationNeverLaunches(t *testing.T) {
	headlessCLIJail(t)
	for _, args := range [][]string{
		{"--files", "one", "two", "--labels", "only-one", "--task", "task"},
		{"--files", filepath.Join(t.TempDir(), "missing"), "--task", "task"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "started")
			binary := writeHeadlessCLIStub(t, "printf started > \""+marker+"\"\nprintf '%s\\n' '{\"result\":\"ok\"}'")
			commandEnv := headlessCLIRuntime(t, binary)
			var stdout, stderr bytes.Buffer
			code := runHeadlessExec(
				append([]string{"--engine", "claude"}, args...),
				strings.NewReader(""),
				&stdout,
				&stderr,
				commandEnv,
			)
			if code != 2 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("invalid file framing launched engine; stat err=%v", err)
			}
		})
	}
}

// TestHeadlessExecReceiptOfAFailedRunCarriesNoEngineOutput pins the promise
// --receipt makes in its own flag help ("content-free"): on a failed run,
// headlessrun.Run's error text splices in up to a KiB each of the engine's
// stdout and stderr tails (run.go's "stderr tail %q; stdout tail %q"), and
// failureDiagnostics repeats both into Result.Diagnostics. Copied verbatim
// into the receipt, that is the model's own answer persisted in the one file
// a lab keeps. The failure must still be visible — on stderr, and as a class
// plus the exit codes in the receipt — but never as engine text.
func TestHeadlessExecReceiptOfAFailedRunCarriesNoEngineOutput(t *testing.T) {
	headlessCLIJail(t)
	const answer = "MODELANSWERLEAK"
	const diagnostic = "ENGINESTDERRLEAK"
	binary := writeHeadlessCLIStub(t, "printf '%s' '"+answer+"'\nprintf '%s' '"+diagnostic+"' >&2\nexit 7")
	commandEnv := headlessCLIRuntime(t, binary)
	receipt := filepath.Join(t.TempDir(), "receipt.jsonl")
	var stdout, stderr bytes.Buffer
	code := runHeadlessExec([]string{
		"--engine", "claude", "--prompt", "leak check", "--output-format", "json", "--receipt", receipt,
	}, strings.NewReader(""), &stdout, &stderr, commandEnv)
	if code != 4 {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want the engine failure", code, stdout.String(), stderr.String())
	}
	body := mustReadCLI(t, receipt)
	for _, leak := range []string{answer, diagnostic} {
		if bytes.Contains(body, []byte(leak)) {
			t.Fatalf("content-free receipt carries engine output %q: %s", leak, body)
		}
	}
	var receiptValue map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(body), &receiptValue); err != nil {
		t.Fatalf("receipt is not JSON: %v", err)
	}
	if value, _ := receiptValue["error"].(string); value == "" {
		t.Fatalf("receipt of a FAILED run names no failure: %s", body)
	}
	if value, _ := receiptValue["engine_exit"].(float64); value != 7 {
		t.Fatalf("receipt engine_exit = %v, want the engine's own 7: %s", receiptValue["engine_exit"], body)
	}
	if !strings.Contains(stderr.String(), answer) {
		t.Fatalf("the full diagnosis left stderr as well as the receipt: %q", stderr.String())
	}
}

func TestHeadlessExecFailureAndTimeoutDoNotWriteOut(t *testing.T) {
	headlessCLIJail(t)
	t.Run("failure", func(t *testing.T) {
		capture := t.TempDir()
		binary := writeHeadlessCLIStub(t, "printf failure >&2\nexit 7")
		commandEnv := headlessCLIRuntime(t, binary)
		out := filepath.Join(t.TempDir(), "out.json")
		receipt := filepath.Join(t.TempDir(), "receipt.jsonl")
		var stdout, stderr bytes.Buffer
		code := runHeadlessExec([]string{
			"--engine", "claude", "--prompt", "failure", "--output-format", "json",
			"--out", out, "--receipt", receipt, "--env", "CAPTURE_DIR=" + capture,
		}, strings.NewReader(""), &stdout, &stderr, commandEnv)
		if code != 4 || !strings.Contains(stderr.String(), "headless run failed") {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatalf("failure wrote output; stat err=%v", err)
		}
		var receiptValue map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(mustReadCLI(t, receipt)), &receiptValue); err != nil {
			t.Fatalf("failure receipt is not JSON: %v", err)
		}
		if value, ok := receiptValue["cost_usd"]; !ok || value != nil {
			t.Fatalf("failure receipt cost = %#v, want explicit null", value)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		binary := writeHeadlessCLIStub(t, "sleep 2\nprintf '%s\\n' '{\"result\":\"late\"}'")
		commandEnv := headlessCLIRuntime(t, binary)
		out := filepath.Join(t.TempDir(), "out.json")
		receipt := filepath.Join(t.TempDir(), "receipt.jsonl")
		var stdout, stderr bytes.Buffer
		code := runHeadlessExec([]string{
			"--engine", "claude", "--prompt", "timeout", "--output-format", "json",
			"--timeout", "0.05", "--out", out, "--receipt", receipt,
		}, strings.NewReader(""), &stdout, &stderr, commandEnv)
		if code != 3 || !strings.Contains(stderr.String(), "timed out") {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatalf("timeout wrote output; stat err=%v", err)
		}
		var receiptValue map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(mustReadCLI(t, receipt)), &receiptValue); err != nil {
			t.Fatalf("timeout receipt is not JSON: %v", err)
		}
		if timeout, ok := receiptValue["timeout"].(bool); !ok || !timeout {
			t.Fatalf("timeout receipt = %#v", receiptValue)
		}
		if value, ok := receiptValue["cost_usd"]; !ok || value != nil {
			t.Fatalf("timeout receipt cost = %#v, want explicit null", value)
		}
	})
}

func TestHeadlessExecConcurrentCallsKeepInputsIndependent(t *testing.T) {
	headlessCLIJail(t)
	binary := writeHeadlessCLIStub(
		t,
		"cat > \"$CAPTURE_DIR/prompt\"\nprintf '%s\\n' '{\"result\":\"ok\",\"total_cost_usd\":null}'",
	)
	var calls sync.WaitGroup
	errs := make(chan error, 2)
	for _, prompt := range []string{"first independent prompt", "second independent prompt"} {
		prompt := prompt
		calls.Add(1)
		go func() {
			defer calls.Done()
			capture := t.TempDir()
			commandEnv := headlessCLIRuntime(t, binary)
			var stdout, stderr bytes.Buffer
			code := runHeadlessExec([]string{
				"--engine", "claude", "--prompt", prompt,
				"--env", "CAPTURE_DIR=" + capture,
			}, strings.NewReader(""), &stdout, &stderr, commandEnv)
			if code != 0 {
				errs <- fmt.Errorf("prompt %q exit=%d stdout=%q stderr=%q", prompt, code, stdout.String(), stderr.String())
				return
			}
			got, err := os.ReadFile(filepath.Join(capture, "prompt"))
			if err != nil {
				errs <- err
				return
			}
			if string(got) != prompt {
				errs <- fmt.Errorf("prompt %q captured %q", prompt, got)
			}
		}()
	}
	calls.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestHeadlessExecCodexSelectorRoutesToOpenCode(t *testing.T) {
	headlessCLIJail(t)
	for _, testCase := range []struct {
		name       string
		selector   string
		wantModel  string
		wantEffort string
	}{
		{name: "codex compatibility selector", selector: "codex", wantModel: "codex-model", wantEffort: "high"},
		{name: "opencode selector", selector: "opencode", wantModel: "opencode-model", wantEffort: "medium"},
		{name: "configured default", selector: "", wantModel: "codex-model", wantEffort: "high"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			capture := t.TempDir()
			binary := writeHeadlessOpenCodeStub(t, `printf '%s\n' "$@" > "$CAPTURE_DIR/args"`)
			configDir := filepath.Join(t.TempDir(), "opencode")
			config := pfmconfig.Config{
				OpenCode:         pfmconfig.OpenCodePrefs{Binary: binary},
				OpenCodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 1, Home: configDir}},
				Ask: pfmconfig.AskConfig{
					Engine: pfmengine.Codex,
					Prefs: map[pfmengine.ID]pfmconfig.EnginePrefs{
						pfmengine.Codex:    {Model: "codex-model", Effort: "high"},
						pfmengine.OpenCode: {Model: "opencode-model", Effort: "medium"},
					},
				},
			}
			if testCase.selector == "" {
				config.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: filepath.Join(t.TempDir(), "codex")}}
			}
			commandEnv := commandRuntime{
				Config: config,
				Paths:  paths.Values{SIDDir: filepath.Join(t.TempDir(), "sid")},
			}
			args := []string{
				"--prompt", "hello", "--output-format", "json",
				"--timeout", "20", "--env", "CAPTURE_DIR=" + capture,
			}
			if testCase.selector != "" {
				args = append(args, "--engine", testCase.selector)
			}
			var stdout, stderr bytes.Buffer
			if code := runHeadlessExec(args, strings.NewReader(""), &stdout, &stderr, commandEnv); code != 0 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			var normalized map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &normalized); err != nil {
				t.Fatalf("normalized output is not JSON: %v (%q)", err, stdout.String())
			}
			if normalized["engine"] != "ox" || normalized["result"] != "opencode answer" {
				t.Fatalf("normalized result = %#v, want the OpenCode ox answer", normalized)
			}
			seen := cliLines(t, filepath.Join(capture, "args"))
			if len(seen) < 3 || seen[0] != "run" || seen[1] != "--attach" ||
				!strings.HasPrefix(seen[2], "http://127.0.0.1:") {
				t.Fatalf("OpenCode argv missing the attached server = %#v", seen)
			}
			want := []string{"--model", testCase.wantModel, "--variant", testCase.wantEffort, "--format", "json"}
			modelIndex := cliIndex(seen, "--model")
			if modelIndex < 0 {
				t.Fatalf("OpenCode argv omitted --model: %#v", seen)
			}
			for index, value := range want {
				if modelIndex+index >= len(seen) || seen[modelIndex+index] != value {
					t.Fatalf("OpenCode argv = %#v, want mapped controls %#v", seen, want)
				}
			}
			if cliHas(seen, "exec") {
				t.Fatalf("the codex compatibility selector launched the native Codex CLI: %#v", seen)
			}
		})
	}
}

func TestHeadlessExecOpenCodeMapsCommonControlsToPrivateEnvironment(t *testing.T) {
	headlessCLIJail(t)
	capture := t.TempDir()
	binary := writeHeadlessOpenCodeStub(t, `printf '%s\n' "$@" > "$CAPTURE_DIR/args"
cat "$PFM_OPENCODE_SYSTEM_FILE" > "$CAPTURE_DIR/system"
cat "$PFM_OPENCODE_SCHEMA_FILE" > "$CAPTURE_DIR/schema"
printf '%s\n' "$PFM_OPENCODE_ALLOWED_TOOLS_JSON" > "$CAPTURE_DIR/permission"
printf '%s\n' "$OPENCODE_CONFIG_CONTENT" > "$CAPTURE_DIR/config"
printf '%s\n' "$XDG_DATA_HOME" > "$CAPTURE_DIR/data-home"`)
	commandEnv := headlessCLIRuntimeFor(t, binary, pfmengine.OpenCode)
	system := filepath.Join(t.TempDir(), "system.txt")
	schema := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(system, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	schemaBody := `{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`
	if err := os.WriteFile(schema, []byte(schemaBody), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runHeadlessExec([]string{
		"--engine", "codex", "--prompt", "hello", "--output-format", "json",
		"--model", "gpt-5.6-luna", "--effort", "high", "--system-file", system,
		"--schema", schema, "--tools", "bash,read", "--setting-sources", "user",
		"--strict-mcp-config", "--no-session-persistence", "--cwd", t.TempDir(),
		"--timeout", "20", "--env", "CAPTURE_DIR=" + capture,
	}, strings.NewReader(""), &stdout, &stderr, commandEnv)
	if code != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	args := cliLines(t, filepath.Join(capture, "args"))
	for _, want := range []string{"run", "--model", "openai/gpt-5.6-luna", "--variant", "high", "--format", "json"} {
		if !cliHas(args, want) {
			t.Fatalf("OpenCode common argv mapping omitted %q: %#v", want, args)
		}
	}
	for _, unwanted := range []string{
		"exec", "--tools", "--setting-sources", "--strict-mcp-config", "--no-session-persistence",
	} {
		if cliHas(args, unwanted) {
			t.Fatalf("a Claude/Codex-only flag leaked into OpenCode argv: %q in %#v", unwanted, args)
		}
	}
	if got := string(mustReadCLI(t, filepath.Join(capture, "system"))); got != "replacement" {
		t.Fatalf("system prompt = %q", got)
	}
	if got := string(mustReadCLI(t, filepath.Join(capture, "schema"))); got != schemaBody {
		t.Fatalf("schema = %q", got)
	}
	var permission []string
	if err := json.Unmarshal(
		bytes.TrimSpace(mustReadCLI(t, filepath.Join(capture, "permission"))),
		&permission,
	); err != nil {
		t.Fatal(err)
	}
	if len(permission) != 3 || !cliHas(permission, "bash") || !cliHas(permission, "read") ||
		!cliHas(permission, "StructuredOutput") {
		t.Fatalf("tool permission mapping = %#v", permission)
	}
	accountDataHome := filepath.Dir(commandEnv.Config.OpenCodeAccounts[0].Home)
	if got := strings.TrimSpace(string(mustReadCLI(t, filepath.Join(capture, "data-home")))); got == accountDataHome {
		t.Fatalf("no-session persistence reused the configured data home %q", got)
	}
}

func cliConfigDir(commandEnv commandRuntime, engine pfmengine.ID) string {
	if engine == pfmengine.Codex {
		return commandEnv.Config.CodexAccounts[0].Home
	}
	if engine == pfmengine.OpenCode {
		return commandEnv.Config.OpenCodeAccounts[0].Home
	}
	return commandEnv.Config.Accounts[0].ConfigDir
}

func cliLines(t *testing.T, path string) []string {
	t.Helper()
	body := strings.TrimSuffix(string(mustReadCLI(t, path)), "\n")
	if body == "" {
		return nil
	}
	return strings.Split(body, "\n")
}

func cliHas(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func cliIndex(values []string, wanted string) int {
	for index, value := range values {
		if value == wanted {
			return index
		}
	}
	return -1
}

func mustReadCLI(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
