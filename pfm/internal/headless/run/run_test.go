package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
)

func headlessJail(t *testing.T) {
	t.Helper()
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	t.Setenv("PFM_SID_DIR", t.TempDir())
	t.Setenv("PFM_HOME", t.TempDir())
}

func writeEngineStub(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engine")
	script := "#!/bin/sh\nset -eu\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func claudeMachine(binary, configDir string) pfmconfig.Config {
	return pfmconfig.Config{
		Claude:   pfmconfig.ClaudePrefs{Binary: binary},
		Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: configDir}},
	}
}

func multiClaudeMachine(topBinary, accountBinary, firstDir, secondDir string) pfmconfig.Config {
	return pfmconfig.Config{
		Claude: pfmconfig.ClaudePrefs{Binary: topBinary},
		Accounts: []pfmconfig.Account{
			{ID: 1, ConfigDir: firstDir},
			{ID: 2, ConfigDir: secondDir, Claude: &pfmconfig.ClaudePrefs{Binary: accountBinary}},
		},
	}
}

func codexMachine(binary, home string) pfmconfig.Config {
	return pfmconfig.Config{
		Codex:         pfmconfig.CodexPrefs{Binary: binary},
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: home}},
	}
}

func testEnv(captureDir string, values ...string) []string {
	environment := append([]string(nil), os.Environ()...)
	environment = append(environment, "CAPTURE_DIR="+captureDir)
	return append(environment, values...)
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	lastState := "unknown"
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		} else if err != nil {
			t.Fatalf("probe descendant process %d: %v", pid, err)
		}

		state, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if err == nil {
			lastState = strings.TrimSpace(string(state))
			if strings.HasPrefix(lastState, "Z") {
				return
			}
		} else {
			lastState = fmt.Sprintf("ps: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant process %d remained live after cancellation (state %s)", pid, lastState)
}

func capturedLines(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = bytes.TrimSuffix(body, []byte("\n"))
	if len(body) == 0 {
		return nil
	}
	return strings.Split(string(body), "\n")
}

func containsPair(args []string, key, value string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == key && args[index+1] == value {
			return true
		}
	}
	return false
}

func stringPtr(value string) *string { return &value }

func TestRunUsesRosterBinaryAndSanitizesParentIdentity(t *testing.T) {
	headlessJail(t)
	parent := t.TempDir()
	capture := t.TempDir()
	wrongMarker := filepath.Join(capture, "wrong")
	wrong := writeEngineStub(t, "printf started > \"$WRONG_MARKER\"")
	right := writeEngineStub(t, `printf '%s\n' "$CLAUDE_CONFIG_DIR" > "$CAPTURE_DIR/config-dir"
printf '%s\n' "$ANTHROPIC_BASE_URL" > "$CAPTURE_DIR/base-url"
printf '%s\n' "$ANTHROPIC_AUTH_TOKEN" > "$CAPTURE_DIR/auth-token"
for name in CLAUDE_CODE_SESSION_ID CLAUDECODE CLAUDE_CODE_CHILD_SESSION CLAUDE_CONFIG_DIR CODEX_THREAD_ID; do
  if printenv "$name" >/dev/null 2>&1; then printf '%s=present\n' "$name" >> "$CAPTURE_DIR/identity"; else printf '%s=absent\n' "$name" >> "$CAPTURE_DIR/identity"; fi
done
printf '%s\n' '{"result":"selected","usage":{"input_tokens":2,"output_tokens":1},"total_cost_usd":null}'`)
	request := Request{
		Config: multiClaudeMachine(wrong, right, filepath.Join(parent, "cc-1"), filepath.Join(parent, "cc-2")),
		Engine: pfmengine.Claude, Account: 2, Prompt: "hello", Env: testEnv(capture,
			"WRONG_MARKER="+wrongMarker,
			"CLAUDE_CODE_SESSION_ID=parent-session", "CLAUDECODE=1", "CLAUDE_CODE_CHILD_SESSION=1",
			"CLAUDE_CONFIG_DIR=parent-config", "CODEX_THREAD_ID=parent-thread",
			"ANTHROPIC_BASE_URL=http://proxy.test", "ANTHROPIC_AUTH_TOKEN=token-test"),
	}
	result, err := Run(context.Background(), request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Answer != "selected" {
		t.Fatalf("answer = %q, want selected", result.Answer)
	}
	if _, err := os.Stat(wrongMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("top-level binary launched instead of account binary; marker err=%v", err)
	}
	if got := strings.TrimSpace(
		string(mustRead(t, filepath.Join(capture, "config-dir"))),
	); got != filepath.Join(
		parent,
		"cc-2",
	) {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want account 2 dir", got)
	}
	for name, path := range map[string]string{"ANTHROPIC_BASE_URL": filepath.Join(capture, "base-url"), "ANTHROPIC_AUTH_TOKEN": filepath.Join(capture, "auth-token")} {
		if got := strings.TrimSpace(string(mustRead(t, path))); got == "" {
			t.Fatalf("explicit %s was dropped", name)
		}
	}
	identity := strings.Join(capturedLines(t, filepath.Join(capture, "identity")), "\n")
	for _, name := range []string{"CLAUDE_CODE_SESSION_ID", "CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CODEX_THREAD_ID"} {
		if !strings.Contains(identity, name+"=absent") {
			t.Fatalf("parent identity leaked through %s: %s", name, identity)
		}
	}
}

func TestRunStripsAmbientProviderSecretsButPreservesExplicitOverrides(t *testing.T) {
	headlessJail(t)
	for _, entry := range []string{
		"ANTHROPIC_API_KEY=ambient-anthropic",
		"OPENAI_API_KEY=ambient-openai",
		"OPENAI_BASE_URL=http://ambient-openai",
		"CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=ambient-simple",
	} {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("invalid fixture %q", entry)
		}
		t.Setenv(name, value)
	}
	capture := t.TempDir()
	t.Setenv("CAPTURE_DIR", capture)
	binary := writeEngineStub(
		t,
		`for name in ANTHROPIC_API_KEY OPENAI_API_KEY OPENAI_BASE_URL CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT; do
  value=$(/usr/bin/printenv "$name" || true)
  printf '%s=%s\n' "$name" "$value" >> "$CAPTURE_DIR/env"
done
printf '%s\n' '{"result":"ok"}'`,
	)
	configDir := filepath.Join(t.TempDir(), "cc")
	machine := claudeMachine(binary, configDir)
	result, err := Run(context.Background(), Request{
		Config: machine, Engine: pfmengine.Claude, Prompt: "ambient",
		Env: nil,
	})
	if err != nil {
		t.Fatalf("ambient Run() error = %v", err)
	}
	if result.Answer != "ok" {
		t.Fatalf("ambient answer = %q", result.Answer)
	}
	for _, line := range capturedLines(t, filepath.Join(capture, "env")) {
		if !strings.HasSuffix(line, "=") {
			t.Fatalf("ambient provider control leaked into child: %q", line)
		}
	}

	if err := os.Remove(filepath.Join(capture, "env")); err != nil {
		t.Fatal(err)
	}
	explicit := []string{
		"CAPTURE_DIR=" + capture,
		"ANTHROPIC_API_KEY=explicit-anthropic",
		"OPENAI_API_KEY=explicit-openai",
		"OPENAI_BASE_URL=http://explicit-openai",
		"CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=0",
		"CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=1",
	}
	if _, err := Run(context.Background(), Request{
		Config: machine, Engine: pfmengine.Claude, Prompt: "explicit", Env: explicit,
	}); err != nil {
		t.Fatalf("explicit Run() error = %v", err)
	}
	values := map[string]string{}
	for _, line := range capturedLines(t, filepath.Join(capture, "env")) {
		name, value, ok := strings.Cut(line, "=")
		if ok {
			values[name] = value
		}
	}
	for name, want := range map[string]string{
		"ANTHROPIC_API_KEY":                "explicit-anthropic",
		"OPENAI_API_KEY":                   "explicit-openai",
		"OPENAI_BASE_URL":                  "http://explicit-openai",
		"CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT": "1",
	} {
		if values[name] != want {
			t.Fatalf("explicit %s = %q, want %q", name, values[name], want)
		}
	}
}

func TestResolveWithoutAccountUsesExplicitEngineHome(t *testing.T) {
	headlessJail(t)
	binary := writeEngineStub(t, "printf '%s\\n' '{\"result\":\"ok\"}'")
	resolved, err := Resolve(Request{
		Config:         pfmconfig.Config{Claude: pfmconfig.ClaudePrefs{Binary: binary}},
		Engine:         pfmengine.Claude,
		WithoutAccount: true,
		Env:            testEnv(t.TempDir(), "CLAUDE_CONFIG_DIR=explicit-config"),
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.ConfigDir != "explicit-config" {
		t.Fatalf("config dir = %q, want explicit engine home", resolved.ConfigDir)
	}
}

// nativeStreamBudget bounds how long a test waits for the native runner's
// first streamed line, derived from the test's own deadline rather than a
// flat literal: a fixed short window turned ordinary fork/exec scheduling
// delay under a loaded suite into a failure that named nothing about
// streaming itself. A genuine streaming regression still never fires the
// signal being waited on, so no budget here lets that regression pass by
// accident — it only decides how long the failure takes to name itself.
func nativeStreamBudget(t *testing.T) time.Duration {
	t.Helper()
	budget := 30 * time.Second
	if deadline, ok := t.Deadline(); ok {
		if remaining := time.Until(deadline) - time.Second; remaining < budget {
			budget = remaining
		}
	}
	return budget
}

func TestRunNativeStreamsStdinAndPreservesUnknownArgs(t *testing.T) {
	headlessJail(t)
	capture := t.TempDir()
	userSchema := filepath.Join(capture, "user-schema.json")
	if err := os.WriteFile(userSchema, []byte(`{"type":"object"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := writeEngineStub(t, `cat > "$CAPTURE_DIR/stdin"
printf '%s\n' "$@" > "$CAPTURE_DIR/args"
printf 'first\n'
sleep 0.25
printf 'second\n'`)
	writer := &observedWriter{first: make(chan struct{})}
	request := Request{
		Config: claudeMachine(binary, filepath.Join(t.TempDir(), "cc")), Engine: pfmengine.Claude,
		Prompt: "unused", Native: true, Env: testEnv(capture), Stdin: strings.NewReader("streamed input\n"),
		Stdout: writer, Args: []string{"--future-flag", "value with spaces", "--output-schema", userSchema},
	}
	done := make(chan struct{})
	var result Result
	var runErr error
	go func() {
		result, runErr = Run(context.Background(), request)
		close(done)
	}()
	select {
	case <-writer.first:
	case <-time.After(nativeStreamBudget(t)):
		t.Fatal("native output did not stream before process completion")
	}
	select {
	case <-done:
		t.Fatal("native runner completed before the delayed output was written")
	case <-time.After(100 * time.Millisecond):
	}
	<-done
	if runErr != nil {
		t.Fatalf("Run() error = %v", runErr)
	}
	if got := writer.String(); got != "first\nsecond\n" {
		t.Fatalf("streamed output = %q", got)
	}
	if got := string(mustRead(t, filepath.Join(capture, "stdin"))); got != "streamed input\n" {
		t.Fatalf("stdin = %q", got)
	}
	args := capturedLines(t, filepath.Join(capture, "args"))
	if !containsPair(args, "--future-flag", "value with spaces") {
		t.Fatalf("unknown native args were not preserved: %#v", args)
	}
	if !containsPair(args, "--output-schema", userSchema) {
		t.Fatalf("native user schema arg was not preserved: %#v", args)
	}
	if _, err := os.Stat(userSchema); err != nil {
		t.Fatalf("native user schema was removed: %v", err)
	}
	if result.Answer != "first\nsecond\n" {
		t.Fatalf("native answer = %q", result.Answer)
	}
}

func TestRunNormalizesClaudeAndCodexWithNullCost(t *testing.T) {
	headlessJail(t)
	schema := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`)
	cases := []struct {
		name   string
		engine pfmengine.ID
		body   string
		config func(string) pfmconfig.Config
		want   string
	}{
		{
			name:   "claude envelope",
			engine: pfmengine.Claude,
			body:   `printf '%s\n' '{"engine":"cc","result":"hello","structured_output":{"answer":"hello"},"usage":{"prompt_tokens":4,"cache_read_input_tokens":2,"completion_tokens":3},"total_cost_usd":null}'`,
			config: func(binary string) pfmconfig.Config { return claudeMachine(binary, filepath.Join(t.TempDir(), "cc")) },
			want:   "hello",
		},
		{
			name:   "codex jsonl",
			engine: pfmengine.Codex,
			body:   `printf '%s\n' '{"type":"thread.started","engine":"cx"}' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"answer\":\"hello\"}"}}' '{"type":"turn.completed","usage":{"input_tokens":4,"cached_input_tokens":2,"output_tokens":3}}'`,
			config: func(binary string) pfmconfig.Config { return codexMachine(binary, filepath.Join(t.TempDir(), "cx")) },
			want:   `{"answer":"hello"}`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			binary := writeEngineStub(t, testCase.body)
			request := Request{
				Config: testCase.config(binary),
				Engine: testCase.engine,
				Prompt: "hello",
				Schema: schema,
			}
			result, err := Run(context.Background(), request)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.Answer != testCase.want {
				t.Fatalf("answer = %q, want %q", result.Answer, testCase.want)
			}
			if result.Engine != testCase.engine {
				t.Fatalf("engine = %q, want canonical %q", result.Engine, testCase.engine)
			}
			if result.Usage == nil || result.Usage.Input != 4 || result.Usage.CachedInput != 2 ||
				result.Usage.Output != 3 {
				t.Fatalf("usage = %#v", result.Usage)
			}
			if result.TotalCostUSD != nil {
				t.Fatalf("absent/null cost became %v, want nil", *result.TotalCostUSD)
			}
			if err := validateInstance(schema, result.StructuredOutput); err != nil {
				t.Fatalf("structured output failed the requested schema: %v", err)
			}
		})
	}
}

func TestCodexReconnectDiagnosticCanRecover(t *testing.T) {
	headlessJail(t)
	binary := writeEngineStub(t, `printf '%s\n' \
  '{"type":"error","message":"Reconnecting... 2/5"}' \
  '{"type":"item.completed","item":{"type":"agent_message","text":"recovered answer"}}' \
  '{"type":"turn.completed"}'`)
	request := Request{
		Config: codexMachine(binary, filepath.Join(t.TempDir(), "cx")),
		Engine: pfmengine.Codex, Prompt: "hello",
	}
	result, err := Run(context.Background(), request)
	if err != nil {
		t.Fatalf("recoverable Codex diagnostic returned error: %v", err)
	}
	if result.Answer != "recovered answer" {
		t.Fatalf("answer = %q, want recovered answer", result.Answer)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0] != "Reconnecting... 2/5" {
		t.Fatalf("diagnostics = %#v, want the reconnect message", result.Diagnostics)
	}
}

func TestCodexDiagnosticAfterCompletionIsFailure(t *testing.T) {
	headlessJail(t)
	binary := writeEngineStub(t, `printf '%s\n' \
  '{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}' \
  '{"type":"turn.completed"}' \
  '{"type":"error","message":"late failure"}'`)
	request := Request{
		Config: codexMachine(binary, filepath.Join(t.TempDir(), "cx")),
		Engine: pfmengine.Codex, Prompt: "hello",
	}
	result, err := Run(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "missing terminal") {
		t.Fatalf("late Codex diagnostic error = %v, want missing terminal failure", err)
	}
	if !result.IsError {
		t.Fatal("late Codex diagnostic returned a successful result")
	}
}

func TestRunReportsMalformedAbsentAndEngineErrorOutputs(t *testing.T) {
	headlessJail(t)
	schema := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`)
	cases := []struct {
		name       string
		engine     pfmengine.ID
		body       string
		withSchema bool
		want       string
	}{
		{"Claude malformed JSON", pfmengine.Claude, "printf '%s\\n' 'not json'", false, "parse Claude JSON envelope"},
		{
			"Claude engine error",
			pfmengine.Claude,
			"printf '%s\\n' '{\"is_error\":true,\"result\":\"no\"}'",
			false,
			"envelope reported an error",
		},
		{
			"Claude missing structured output",
			pfmengine.Claude,
			"printf '%s\\n' '{\"result\":\"plain\"}'",
			true,
			"structured_output",
		},
		{"Claude malformed result", pfmengine.Claude, "printf '%s\\n' '{\"result\":123}'", false, ""},
		{
			"Claude malformed usage",
			pfmengine.Claude,
			"printf '%s\\n' '{\"result\":\"plain\",\"usage\":\"bad\"}'",
			false,
			"",
		},
		{"Codex malformed JSONL", pfmengine.Codex, "printf '%s\\n' 'not json'", false, "parse Codex JSONL"},
		{
			"Codex missing terminal",
			pfmengine.Codex,
			"printf '%s\\n' '{\"type\":\"item\",\"item\":{\"type\":\"agent_message\",\"text\":\"plain\"}}'",
			false,
			"missing terminal",
		},
		{
			"Codex started agent message",
			pfmengine.Codex,
			"printf '%s\\n' '{\"type\":\"item.started\",\"item\":{\"type\":\"agent_message\",\"text\":\"started\"}}' '{\"type\":\"turn.completed\"}'",
			false,
			"",
		},
		{"Codex terminal without answer", pfmengine.Codex, "printf '%s\\n' '{\"type\":\"turn.completed\"}'", false, ""},
		{
			"Codex engine error",
			pfmengine.Codex,
			"printf '%s\\n' '{\"type\":\"turn.failed\"}'",
			false,
			"event turn.failed",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			binary := writeEngineStub(t, testCase.body)
			var machine pfmconfig.Config
			if testCase.engine == pfmengine.Claude {
				machine = claudeMachine(binary, filepath.Join(t.TempDir(), "cc"))
			} else {
				machine = codexMachine(binary, filepath.Join(t.TempDir(), "cx"))
			}
			request := Request{Config: machine, Engine: testCase.engine, Prompt: "hello"}
			if testCase.withSchema {
				request.Schema = schema
			}
			result, err := Run(context.Background(), request)
			if err == nil || (testCase.want != "" && !strings.Contains(err.Error(), testCase.want)) {
				t.Fatalf("error = %v, want substring %q", err, testCase.want)
			}
			if !result.IsError {
				t.Fatal("malformed/absent/error output returned without an error state")
			}
		})
	}
}

func TestSealedClaudeOwnsScratchAndEmptyInheritedControls(t *testing.T) {
	headlessJail(t)
	base := t.TempDir()
	capture := t.TempDir()
	requestedCWD := t.TempDir()
	binary := writeEngineStub(t, `pwd > "$CAPTURE_DIR/pwd"
printf '%s\n' "$@" > "$CAPTURE_DIR/args"
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--system-prompt-file" ]; then cat "$2" > "$CAPTURE_DIR/system"; fi
  shift
done
printf '%s\n' '{"result":"sealed"}'`)
	system := "replacement system"
	request := Request{
		Config: claudeMachine(binary, filepath.Join(base, "account-config")), Engine: pfmengine.Claude,
		Prompt: "hello", CWD: requestedCWD, TempDir: base, SystemPrompt: &system, Sealed: true,
		Env: testEnv(capture, "CLAUDE_CONFIG_DIR=parent-config", "CLAUDECODE=parent"),
	}
	result, err := Run(context.Background(), request)
	if err != nil {
		t.Fatalf("sealed Run() error = %v", err)
	}
	if result.Answer != "sealed" {
		t.Fatalf("answer = %q", result.Answer)
	}
	pwd := strings.TrimSpace(string(mustRead(t, filepath.Join(capture, "pwd"))))
	if filepath.Clean(pwd) == filepath.Clean(requestedCWD) || !insideTempBase(t, pwd, base) {
		t.Fatalf("sealed cwd = %q, want a temporary child of %q", pwd, base)
	}
	if _, err := os.Stat(pwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sealed scratch cwd was not removed: stat err=%v", err)
	}
	args := capturedLines(t, filepath.Join(capture, "args"))
	for _, pair := range [][2]string{{"--tools", ""}, {"--setting-sources", ""}} {
		if !containsPair(args, pair[0], pair[1]) {
			t.Fatalf("sealed args missing %s=%q: %#v", pair[0], pair[1], args)
		}
	}
	if !contains(args, "--system-prompt-file") {
		t.Fatalf("sealed args missing system prompt file: %#v", args)
	}
	if got := strings.TrimSpace(string(mustRead(t, filepath.Join(capture, "system")))); got != system {
		t.Fatalf("sealed system prompt = %q, want %q", got, system)
	}
	for _, flag := range []string{"--strict-mcp-config", "--no-session-persistence", "--safe-mode"} {
		if !contains(args, flag) {
			t.Fatalf("sealed args missing %s: %#v", flag, args)
		}
	}
}

func TestCodexIsolationCapabilityErrorsHappenBeforeLaunch(t *testing.T) {
	headlessJail(t)
	cases := []struct {
		name   string
		mutate func(*Request)
	}{
		{"tools", func(request *Request) { value := "none"; request.Tools = &value }},
		{"settings", func(request *Request) { value := ""; request.SettingsSources = &value }},
		{"strict MCP", func(request *Request) { request.StrictMCP = true }},
		{"sealed", func(request *Request) { request.Sealed = true; value := "system"; request.SystemPrompt = &value }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			capture := t.TempDir()
			binary := writeEngineStub(t, "printf started > \"$CAPTURE_DIR/started\"")
			request := Request{
				Config: codexMachine(binary, filepath.Join(t.TempDir(), "cx")),
				Engine: pfmengine.Codex,
				Prompt: "hello",
				Env:    testEnv(capture),
			}
			testCase.mutate(&request)
			_, err := Run(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), "headless runs with Codex") {
				t.Fatalf("error = %v, want an explicit Codex capability error", err)
			}
			if _, statErr := os.Stat(filepath.Join(capture, "started")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("Codex capability refusal launched the engine; stat err=%v", statErr)
			}
		})
	}
}

func TestCodexAllowUnsupportedKeepsSupportedControlsAndReportsDiagnostics(t *testing.T) {
	headlessJail(t)
	capture := t.TempDir()
	binary := writeEngineStub(t, `printf '%s\n' "$@" > "$CAPTURE_DIR/args"
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"ok\":true}"}}' '{"type":"turn.completed"}'`)
	system := "supported system"
	request := Request{
		Config:       codexMachine(binary, filepath.Join(t.TempDir(), "cx")),
		Engine:       pfmengine.Codex,
		Prompt:       "hello",
		SystemPrompt: &system,
		Schema: json.RawMessage(
			`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`,
		),
		Tools:                stringPtr("none"),
		SettingsSources:      stringPtr(""),
		StrictMCP:            true,
		NoSessionPersistence: true,
		AllowUnsupported:     true,
		Env:                  testEnv(capture),
	}
	result, err := Run(context.Background(), request)
	if err != nil {
		t.Fatalf("opted-in Codex run failed: %v", err)
	}
	if err := validateInstance(request.Schema, result.StructuredOutput); err != nil {
		t.Fatalf("supported schema rejected: %v", err)
	}
	for _, want := range []string{
		"unsupported control not applied for Codex: --tools",
		"unsupported control not applied for Codex: --setting-sources",
		"unsupported control not applied for Codex: --strict-mcp-config",
	} {
		if !contains(result.Diagnostics, want) {
			t.Fatalf("diagnostics omitted %q: %#v", want, result.Diagnostics)
		}
	}
	args := capturedLines(t, filepath.Join(capture, "args"))
	for _, forbidden := range []string{"--tools", "--setting-sources", "--strict-mcp-config"} {
		if contains(args, forbidden) {
			t.Fatalf("unsupported control reached Codex argv: %q in %#v", forbidden, args)
		}
	}
	if !contains(args, "--ephemeral") {
		t.Fatalf("supported no-session persistence control missing: %#v", args)
	}
	if !contains(args, "-c") || !strings.Contains(strings.Join(args, " "), "model_instructions_file=") {
		t.Fatalf("supported system prompt mapping missing: %#v", args)
	}
}

func TestCodexSealedAllowUnsupportedKeepsScratchAndWarnings(t *testing.T) {
	headlessJail(t)
	base := t.TempDir()
	capture := t.TempDir()
	binary := writeEngineStub(t, `pwd > "$CAPTURE_DIR/pwd"
printf '%s\n' "$@" > "$CAPTURE_DIR/args"
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"sealed"}}' '{"type":"turn.completed"}'`)
	system := "sealed replacement"
	result, err := Run(context.Background(), Request{
		Config: codexMachine(binary, filepath.Join(base, "cx")), Engine: pfmengine.Codex,
		Prompt: "hello", TempDir: base, SystemPrompt: &system, Sealed: true,
		AllowUnsupported: true, Args: []string{"-c", "openai_base_url=http://proxy"}, Env: testEnv(capture),
	})
	if err != nil {
		t.Fatalf("sealed opt-in Codex run failed: %v", err)
	}
	pwd := strings.TrimSpace(string(mustRead(t, filepath.Join(capture, "pwd"))))
	if !insideTempBase(t, pwd, base) {
		t.Fatalf("sealed Codex cwd = %q, want child of %q", pwd, base)
	}
	if _, err := os.Stat(pwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sealed Codex scratch cwd was not removed: %v", err)
	}
	args := capturedLines(t, filepath.Join(capture, "args"))
	for _, want := range []string{"--ephemeral", "--ignore-user-config", "--ignore-rules"} {
		if !contains(args, want) {
			t.Fatalf("sealed Codex argv missing %q: %#v", want, args)
		}
	}
	if !containsPair(args, "-c", "openai_base_url=http://proxy") {
		t.Fatalf("sealed Codex opt-in did not preserve native proxy argument: %#v", args)
	}
	for _, forbidden := range []string{"--tools", "--setting-sources", "--strict-mcp-config"} {
		if contains(args, forbidden) {
			t.Fatalf("sealed unsupported control reached Codex argv: %q in %#v", forbidden, args)
		}
	}
	if !contains(result.Diagnostics, "unsupported control not applied for Codex: --sealed (full isolation)") {
		t.Fatalf("sealed warning missing: %#v", result.Diagnostics)
	}
}

// armedDeadlineContext is a context.Context whose Err() reports
// context.DeadlineExceeded only once arm() is called. It lets a test decide
// the exact instant a "timeout" fires instead of racing a fixed wall-clock
// duration against a child process's fork + pid-file write, while still
// exercising the same ctx.Done()-driven process-group kill path a real
// request.Timeout deadline would.
type armedDeadlineContext struct {
	done chan struct{}
	mu   sync.Mutex
	err  error
}

func newArmedDeadlineContext() *armedDeadlineContext {
	return &armedDeadlineContext{done: make(chan struct{})}
}

func (c *armedDeadlineContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *armedDeadlineContext) Done() <-chan struct{}       { return c.done }
func (c *armedDeadlineContext) Value(any) any               { return nil }
func (c *armedDeadlineContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *armedDeadlineContext) arm() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = context.DeadlineExceeded
		close(c.done)
	}
}

// waitForFile waits for path to exist AND be non-empty. Existence alone is
// not enough: the shell redirection that creates it (`echo "$!" > path`)
// opens/truncates the file before the write lands, so a poll that only
// checks os.Stat can observe the file mid-creation — empty — and hand the
// caller zero bytes to parse. That race was this helper's whole bug: under
// load the open/write gap widens and TestRunTimeoutKillsProcessGroup read an
// empty child-pid file ("child pid \"\": EOF") for a reason that has nothing
// to do with the timeout/kill behavior under test.
func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("file %s did not appear with content within %s", path, timeout)
}

func TestRunTimeoutKillsProcessGroup(t *testing.T) {
	headlessJail(t)
	capture := t.TempDir()
	binary := writeEngineStub(t, `(sleep 30) &
echo "$!" > "$CAPTURE_DIR/child-pid"
sleep 30`)
	request := Request{
		Config: claudeMachine(binary, filepath.Join(t.TempDir(), "cc")),
		Engine: pfmengine.Claude,
		Prompt: "hello",
		Env:    testEnv(capture),
	}
	ctx := newArmedDeadlineContext()
	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	started := time.Now()
	go func() {
		result, err := Run(ctx, request)
		done <- outcome{result, err}
	}()

	pidPath := filepath.Join(capture, "child-pid")
	waitForFile(t, pidPath, 3*time.Second)
	ctx.arm()

	var out outcome
	select {
	case out = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run() did not return after the deadline was armed")
	}
	if out.err == nil || !errors.Is(out.err, context.DeadlineExceeded) {
		t.Fatalf("timeout err = %v, result=%#v", out.err, out.result)
	}
	if !out.result.TimedOut {
		t.Fatal("timeout result did not mark timeout")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("timeout waited for descendant pipe holder: %s", elapsed)
	}
	pidText := strings.TrimSpace(string(mustRead(t, pidPath)))
	var pid int
	if _, scanErr := fmt.Sscanf(pidText, "%d", &pid); scanErr != nil {
		t.Fatalf("child pid %q: %v", pidText, scanErr)
	}
	waitForProcessExit(t, pid)
}

func TestSchemaFileIsRemovedAfterCodexRun(t *testing.T) {
	headlessJail(t)
	capture := t.TempDir()
	binary := writeEngineStub(t, `printf '%s\n' "$@" > "$CAPTURE_DIR/args"
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"answer\":\"ok\"}"}}' '{"type":"turn.completed"}'`)
	request := Request{
		Config: codexMachine(binary, filepath.Join(t.TempDir(), "cx")), Engine: pfmengine.Codex,
		Prompt: "hello", Schema: json.RawMessage(`{"type":"object"}`), Env: testEnv(capture),
	}
	if _, err := Run(context.Background(), request); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	args := capturedLines(t, filepath.Join(capture, "args"))
	index := indexOf(args, "--output-schema")
	if index < 0 || index+1 >= len(args) {
		t.Fatalf("output schema path missing from args: %#v", args)
	}
	if _, err := os.Stat(args[index+1]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary schema %q was not removed: stat err=%v", args[index+1], err)
	}
}

func TestArgumentsAllowFutureFlagsAndValidateSchemaBeforeLaunch(t *testing.T) {
	headlessJail(t)
	args, err := arguments(
		Request{
			Engine: pfmengine.Claude,
			Args:   []string{"--new-future-flag", "value"},
			Schema: json.RawMessage(`{"type":"string"}`),
		},
	)
	if err != nil {
		t.Fatalf("arguments() error = %v", err)
	}
	if !containsPair(args, "--new-future-flag", "value") {
		t.Fatalf("future args rejected or reordered: %#v", args)
	}
	if !containsPair(args, "--json-schema", `{"type":"string"}`) {
		t.Fatalf("Claude schema was not passed losslessly: %#v", args)
	}
	binary := writeEngineStub(t, "printf '%s\\n' '{\"result\":\"ok\"}'")
	if _, err := Resolve(
		Request{
			Config: claudeMachine(binary, filepath.Join(t.TempDir(), "cc")),
			Engine: pfmengine.Claude,
			Schema: json.RawMessage(`not-json`),
		},
	); err == nil ||
		!strings.Contains(err.Error(), "invalid output schema") {
		t.Fatalf("invalid schema was accepted: %v", err)
	}
}

type observedWriter struct {
	mu    sync.Mutex
	data  bytes.Buffer
	first chan struct{}
	once  sync.Once
}

func (writer *observedWriter) Write(value []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	_, _ = writer.data.Write(value)
	writer.once.Do(func() { close(writer.first) })
	return len(value), nil
}

func (writer *observedWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.data.String()
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func indexOf(values []string, wanted string) int {
	for index, value := range values {
		if value == wanted {
			return index
		}
	}
	return -1
}

// insideTempBase reports whether a captured working directory lies under base.
//
// The child reports its cwd PHYSICALLY: on macOS /var is a symlink to
// /private/var, so a scratch dir Go created under a /var/folders/… temp base
// comes back spelled /private/var/folders/…. Comparing the two spellings
// directly fails for a reason that has nothing to do with sealing — the very
// behavior under test — so base is resolved through the same symlinks before
// the prefix test. It is base that gets resolved and not the cwd because the
// scratch dir is already gone by the time this runs; that removal is the next
// assertion at every call site.
func insideTempBase(t *testing.T, pwd, base string) bool {
	t.Helper()
	candidates := []string{filepath.Clean(base)}
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		candidates = append(candidates, filepath.Clean(resolved))
	}
	cleaned := filepath.Clean(pwd)
	for _, candidate := range candidates {
		if strings.HasPrefix(cleaned, candidate+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}
