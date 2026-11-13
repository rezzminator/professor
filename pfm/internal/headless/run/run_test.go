package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func headlessJail(t *testing.T) {
	t.Helper()
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	t.Setenv("PFM_SID_DIR", t.TempDir())
	t.Setenv("PFM_HOME", t.TempDir())
}

func claudeMachine(binary, configDir string) pfmconfig.Config {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		panic(fmt.Sprintf("fixture account dir %s: %v", configDir, err))
	}
	return pfmconfig.Config{
		Claude: pfmconfig.ClaudePrefs{Binary: binary}, Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: configDir}},
	}
}

func multiClaudeMachine(topBinary, accountBinary, firstDir, secondDir string) pfmconfig.Config {
	for _, dir := range []string{firstDir, secondDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			panic(fmt.Sprintf("fixture account dir %s: %v", dir, err))
		}
	}
	return pfmconfig.Config{
		Claude: pfmconfig.ClaudePrefs{Binary: topBinary},
		Accounts: []pfmconfig.Account{
			{ID: 1, ConfigDir: firstDir},
			{ID: 2, ConfigDir: secondDir, Claude: &pfmconfig.ClaudePrefs{Binary: accountBinary}},
		},
	}
}

func codexMachine(binary, home string) pfmconfig.Config {
	if err := os.MkdirAll(home, 0o700); err != nil {
		panic(fmt.Sprintf("fixture Codex home %s: %v", home, err))
	}
	if err := os.WriteFile(
		filepath.Join(home, "auth.json"),
		[]byte(`{"tokens":{"access_token":"fixture","account_id":"fixture"}}`),
		0o600,
	); err != nil {
		panic(fmt.Sprintf("fixture Codex auth %s: %v", home, err))
	}
	return pfmconfig.Config{
		Codex: pfmconfig.CodexPrefs{Binary: binary}, CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: home}},
	}
}

func testEnv(captureDir string, values ...string) []string {
	environment := append([]string(nil), os.Environ()...)
	environment = append(environment, "CAPTURE_DIR="+captureDir)
	return append(environment, values...)
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
	wrong := engineBinary(t)
	right := engineBinary(t)
	runner := fakeEngine(deps.StartOutput{
		Stdout: `{"result":"selected","usage":{"input_tokens":2,"output_tokens":1},"total_cost_usd":null}` + "\n",
	})
	request := Request{
		Config: multiClaudeMachine(wrong, right, filepath.Join(parent, "cc-1"), filepath.Join(parent, "cc-2")),
		Engine: pfmengine.Claude, Account: 2, Prompt: "hello", Runner: runner, Env: hostEnv(
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
	start := onlyStart(t, runner)
	if start.Argv[0] != right {
		t.Fatalf("launched %q, want the account binary %q", start.Argv[0], right)
	}
	if got := lastEnvironmentValue(start.Opts.Env, "CLAUDE_CONFIG_DIR"); got != filepath.Join(parent, "cc-2") {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want account 2 dir", got)
	}
	for name, want := range map[string]string{"ANTHROPIC_BASE_URL": "http://proxy.test", "ANTHROPIC_AUTH_TOKEN": "token-test"} {
		if got := lastEnvironmentValue(start.Opts.Env, name); got != want {
			t.Fatalf("explicit %s = %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{"CLAUDE_CODE_SESSION_ID", "CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CODEX_THREAD_ID"} {
		if envHas(start.Opts.Env, name) {
			t.Fatalf("parent identity leaked through %s: %q", name, lastEnvironmentValue(start.Opts.Env, name))
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
	runner := fakeEngine(deps.StartOutput{Stdout: `{"result":"ok"}` + "\n"})
	configDir := filepath.Join(t.TempDir(), "cc")
	machine := claudeMachine(engineBinary(t), configDir)
	result, err := Run(context.Background(), Request{
		Config: machine, Engine: pfmengine.Claude, Prompt: "ambient", Env: nil, Runner: runner,
	})
	if err != nil {
		t.Fatalf("ambient Run() error = %v", err)
	}
	if result.Answer != "ok" {
		t.Fatalf("ambient answer = %q", result.Answer)
	}
	ambient := onlyStart(t, runner).Opts.Env
	if got := lastEnvironmentValue(ambient, "CLAUDE_CONFIG_DIR"); got != configDir {
		t.Fatalf("ambient run CLAUDE_CONFIG_DIR = %q, want %q", got, configDir)
	}
	for _, name := range []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENAI_BASE_URL", "CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT",
	} {
		if value := lastEnvironmentValue(ambient, name); value != "" {
			t.Fatalf("ambient provider control leaked into child: %s=%q", name, value)
		}
	}
	explicit := []string{
		"ANTHROPIC_API_KEY=explicit-anthropic",
		"OPENAI_API_KEY=explicit-openai",
		"OPENAI_BASE_URL=http://explicit-openai",
		"CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=0",
		"CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=1",
	}
	explicitRunner := fakeEngine(deps.StartOutput{Stdout: `{"result":"ok"}` + "\n"})
	if _, err := Run(context.Background(), Request{
		Config: machine, Engine: pfmengine.Claude, Prompt: "explicit", Env: explicit, Runner: explicitRunner,
	}); err != nil {
		t.Fatalf("explicit Run() error = %v", err)
	}
	values := onlyStart(t, explicitRunner).Opts.Env
	for name, want := range map[string]string{
		"ANTHROPIC_API_KEY":                "explicit-anthropic",
		"OPENAI_API_KEY":                   "explicit-openai",
		"OPENAI_BASE_URL":                  "http://explicit-openai",
		"CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT": "1",
	} {
		if got := lastEnvironmentValue(values, name); got != want {
			t.Fatalf("explicit %s = %q, want %q", name, got, want)
		}
	}
}

func TestResolveWithoutAccountUsesExplicitEngineHome(t *testing.T) {
	configDir := t.TempDir()
	headlessJail(t)
	binary := engineBinary(t)
	resolved, err := Resolve(Request{
		Config: pfmconfig.Config{Claude: pfmconfig.ClaudePrefs{Binary: binary}}, Engine: pfmengine.Claude,
		WithoutAccount: true, Env: hostEnv("CLAUDE_CONFIG_DIR=" + configDir),
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.ConfigDir != configDir {
		t.Fatalf("config dir = %q, want explicit engine home", resolved.ConfigDir)
	}
}

func TestRunNormalizesClaudeAndCodexWithNullCost(t *testing.T) {
	headlessJail(t)
	schema := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`)
	cases := []struct {
		name   string
		engine pfmengine.ID
		stdout string
		config func(string) pfmconfig.Config
		want   string
	}{
		{
			name:   "claude envelope",
			engine: pfmengine.Claude,
			stdout: `{"engine":"cc","result":"hello","structured_output":{"answer":"hello"},"usage":{"prompt_tokens":4,"cache_read_input_tokens":2,"completion_tokens":3},"total_cost_usd":null}` + "\n",
			config: func(binary string) pfmconfig.Config { return claudeMachine(binary, filepath.Join(t.TempDir(), "cc")) },
			want:   "hello",
		},
		{
			name:   "codex jsonl",
			engine: pfmengine.Codex,
			stdout: `{"type":"thread.started","engine":"cx"}` + "\n" +
				`{"type":"item.completed","item":{"type":"agent_message","text":"{\"answer\":\"hello\"}"}}` + "\n" +
				`{"type":"turn.completed","usage":{"input_tokens":4,"cached_input_tokens":2,"output_tokens":3}}` + "\n",
			config: func(binary string) pfmconfig.Config { return codexMachine(binary, filepath.Join(t.TempDir(), "cx")) },
			want:   `{"answer":"hello"}`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := Request{
				Config: testCase.config(engineBinary(t)), Engine: testCase.engine, Prompt: "hello", Schema: schema,
				Runner: fakeEngine(deps.StartOutput{Stdout: testCase.stdout}),
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
	request := Request{
		Config: codexMachine(engineBinary(t), filepath.Join(t.TempDir(), "cx")),
		Engine: pfmengine.Codex, Prompt: "hello",
		Runner: fakeEngine(deps.StartOutput{Stdout: `{"type":"error","message":"Reconnecting... 2/5"}` + "\n" +
			`{"type":"item.completed","item":{"type":"agent_message","text":"recovered answer"}}` + "\n" +
			`{"type":"turn.completed"}` + "\n"}),
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
	request := Request{
		Config: codexMachine(engineBinary(t), filepath.Join(t.TempDir(), "cx")),
		Engine: pfmengine.Codex,
		Prompt: "hello",
		Runner: fakeEngine(
			deps.StartOutput{
				Stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}` + "\n" +
					`{"type":"turn.completed"}` + "\n" +
					`{"type":"error","message":"late failure"}` + "\n",
			},
		),
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
		stdout     string
		withSchema bool
		want       string
	}{
		{"Claude malformed JSON", pfmengine.Claude, "not json\n", false, "parse Claude JSON envelope"},
		{
			"Claude engine error",
			pfmengine.Claude,
			`{"is_error":true,"result":"no"}` + "\n",
			false,
			"envelope reported an error",
		},
		{"Claude missing structured output", pfmengine.Claude, `{"result":"plain"}` + "\n", true, "structured_output"},
		{"Codex malformed JSONL", pfmengine.Codex, "not json\n", false, "missing terminal"},
		{
			"Codex missing terminal",
			pfmengine.Codex,
			`{"type":"item","item":{"type":"agent_message","text":"plain"}}` + "\n",
			false,
			"missing terminal",
		},
		{
			"Codex started agent message",
			pfmengine.Codex,
			`{"type":"item.started","item":{"type":"agent_message","text":"started"}}` + "\n" +
				`{"type":"turn.completed"}` + "\n",
			false,
			"",
		},
		{"Codex terminal without answer", pfmengine.Codex, `{"type":"turn.completed"}` + "\n", false, ""},
		{"Codex engine error", pfmengine.Codex, `{"type":"turn.failed"}` + "\n", false, "event turn.failed"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			binary := engineBinary(t)
			var machine pfmconfig.Config
			if testCase.engine == pfmengine.Claude {
				machine = claudeMachine(binary, filepath.Join(t.TempDir(), "cc"))
			} else {
				machine = codexMachine(binary, filepath.Join(t.TempDir(), "cx"))
			}
			request := Request{
				Config: machine, Engine: testCase.engine, Prompt: "hello",
				Runner: fakeEngine(deps.StartOutput{Stdout: testCase.stdout}),
			}
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
			// output pfm could not read is shown, never dropped
			if testCase.name == "Claude malformed JSON" &&
				!reflect.DeepEqual(result.Diagnostics, []string{"stdout: not json"}) {
				t.Fatalf("diagnostics = %#v, want the unread stdout", result.Diagnostics)
			}
		})
	}
}

func TestSealedClaudeOwnsScratchAndEmptyInheritedControls(t *testing.T) {
	headlessJail(t)
	base := t.TempDir()
	requestedCWD := t.TempDir()
	system := "replacement system"
	var systemSeen string
	runner := fakeEngine(deps.StartOutput{
		Stdout: `{"result":"sealed"}` + "\n",
		AtStart: func(argv []string, _ deps.StartOptions) {
			for index := 0; index+1 < len(argv); index++ {
				if argv[index] == "--system-prompt-file" {
					body, err := os.ReadFile(argv[index+1])
					if err != nil {
						t.Errorf("read the system prompt file the engine was given: %v", err)
					}
					systemSeen = strings.TrimSpace(string(body))
				}
			}
		},
	})
	request := Request{
		Config: claudeMachine(engineBinary(t), filepath.Join(base, "account-config")), Engine: pfmengine.Claude,
		Prompt: "hello", CWD: requestedCWD, TempDir: base, SystemPrompt: &system, Sealed: true, Runner: runner,
		Env: hostEnv("CLAUDE_CONFIG_DIR=parent-config", "CLAUDECODE=parent"),
	}
	result, err := Run(context.Background(), request)
	if err != nil {
		t.Fatalf("sealed Run() error = %v", err)
	}
	if result.Answer != "sealed" {
		t.Fatalf("answer = %q", result.Answer)
	}
	start := onlyStart(t, runner)
	pwd := start.Opts.Dir
	if filepath.Clean(pwd) == filepath.Clean(requestedCWD) || !insideTempBase(t, pwd, base) {
		t.Fatalf("sealed cwd = %q, want a temporary child of %q", pwd, base)
	}
	if _, err := os.Stat(pwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sealed scratch cwd was not removed: stat err=%v", err)
	}
	args := start.Argv[1:]
	for _, pair := range [][2]string{{"--tools", ""}, {"--setting-sources", ""}} {
		if !containsPair(args, pair[0], pair[1]) {
			t.Fatalf("sealed args missing %s=%q: %#v", pair[0], pair[1], args)
		}
	}
	if !contains(args, "--system-prompt-file") {
		t.Fatalf("sealed args missing system prompt file: %#v", args)
	}
	if systemSeen != system {
		t.Fatalf("sealed system prompt = %q, want %q", systemSeen, system)
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
			runner := fakeEngine(deps.StartOutput{})
			request := Request{
				Config: codexMachine(engineBinary(t), filepath.Join(t.TempDir(), "cx")), Engine: pfmengine.Codex,
				Prompt: "hello", Env: hostEnv(), Runner: runner,
			}
			testCase.mutate(&request)
			_, err := Run(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), "headless runs with Codex") {
				t.Fatalf("error = %v, want an explicit Codex capability error", err)
			}
			if starts := runner.Starts(); len(starts) != 0 {
				t.Fatalf("Codex capability refusal launched the engine: %v", starts)
			}
		})
	}
}

func TestCodexAllowUnsupportedKeepsSupportedControlsAndReportsDiagnostics(t *testing.T) {
	headlessJail(t)
	runner := fakeEngine(deps.StartOutput{
		Stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"{\"ok\":true}"}}` + "\n" +
			`{"type":"turn.completed"}` + "\n",
	})
	system := "supported system"
	request := Request{
		Config: codexMachine(engineBinary(t), filepath.Join(t.TempDir(), "cx")),
		Engine: pfmengine.Codex, Prompt: "hello",
		SystemPrompt: &system,
		Schema: json.RawMessage(
			`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`,
		),
		Tools: stringPtr("none"), SettingsSources: stringPtr(""), StrictMCP: true, NoSessionPersistence: true,
		AllowUnsupported: true, Env: hostEnv(), Runner: runner,
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
	args := onlyStart(t, runner).Argv[1:]
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
	runner := fakeEngine(deps.StartOutput{
		Stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"sealed"}}` + "\n" +
			`{"type":"turn.completed"}` + "\n",
	})
	system := "sealed replacement"
	result, err := Run(context.Background(), Request{
		Config: codexMachine(engineBinary(t), filepath.Join(base, "cx")), Engine: pfmengine.Codex,
		Prompt: "hello", TempDir: base, SystemPrompt: &system, Sealed: true,
		AllowUnsupported: true, Args: []string{"-c", "openai_base_url=http://proxy"}, Env: hostEnv(),
		Runner: runner,
	})
	if err != nil {
		t.Fatalf("sealed opt-in Codex run failed: %v", err)
	}
	start := onlyStart(t, runner)
	pwd := start.Opts.Dir
	if !insideTempBase(t, pwd, base) {
		t.Fatalf("sealed Codex cwd = %q, want child of %q", pwd, base)
	}
	if _, err := os.Stat(pwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sealed Codex scratch cwd was not removed: %v", err)
	}
	args := start.Argv[1:]
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

func TestSchemaFileIsRemovedAfterCodexRun(t *testing.T) {
	headlessJail(t)
	runner := fakeEngine(deps.StartOutput{
		Stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"{\"answer\":\"ok\"}"}}` + "\n" +
			`{"type":"turn.completed"}` + "\n",
	})
	request := Request{
		Config: codexMachine(engineBinary(t), filepath.Join(t.TempDir(), "cx")), Engine: pfmengine.Codex,
		Prompt: "hello", Schema: json.RawMessage(`{"type":"object"}`), Env: hostEnv(), Runner: runner,
	}
	if _, err := Run(context.Background(), request); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	args := onlyStart(t, runner).Argv[1:]
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
			Engine: pfmengine.Claude, Args: []string{"--new-future-flag", "value"},
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
	binary := engineBinary(t)
	if _, err := Resolve(
		Request{
			Config: claudeMachine(binary, filepath.Join(t.TempDir(), "cc")), Engine: pfmengine.Claude,
			Schema: json.RawMessage(`not-json`),
		},
	); err == nil ||
		!strings.Contains(err.Error(), "invalid output schema") {
		t.Fatalf("invalid schema was accepted: %v", err)
	}
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

func TestClaudeMachineAccountDirs(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		t.Run(fmt.Sprint(multiple), func(t *testing.T) {
			first, second := filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "second")
			machine := claudeMachine("claude", first)
			if multiple {
				machine = multiClaudeMachine("claude", "claude", first, second)
			}
			for _, account := range machine.Accounts {
				info, err := os.Stat(account.ConfigDir)
				if err != nil {
					t.Fatal(err)
				}
				if !info.IsDir() || info.Mode().Perm() != 0o700 {
					t.Fatalf("account dir %s: mode %v", account.ConfigDir, info.Mode())
				}
			}
		})
	}
}

func TestClaudeMachineAccountDirFailure(t *testing.T) {
	for index, makeMachine := range []func(string) pfmconfig.Config{
		func(dir string) pfmconfig.Config { return claudeMachine("claude", dir) },
		func(dir string) pfmconfig.Config { return multiClaudeMachine("claude", "claude", t.TempDir(), dir) },
	} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			blocked := filepath.Join(t.TempDir(), "blocked")
			if err := os.WriteFile(blocked, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			wantErr := os.MkdirAll(blocked, 0o700)
			defer func() {
				got := fmt.Sprint(recover())
				want := fmt.Sprintf("fixture account dir %s: %v", blocked, wantErr)
				if got != want {
					t.Fatalf("fixture panic = %q, want %q", got, want)
				}
			}()
			makeMachine(blocked)
		})
	}
}

func TestRunCodexLoginSelection(t *testing.T) {
	for _, name := range []string{"native roster logged out", "without account"} {
		t.Run(name, func(t *testing.T) {
			headlessJail(t)
			home := t.TempDir()
			runner := fakeEngine(deps.StartOutput{})
			request := Request{
				Engine: pfmengine.Codex, Native: true, Prompt: "hello", Runner: runner,
				Config: pfmconfig.Config{Codex: pfmconfig.CodexPrefs{Binary: engineBinary(t)}},
				Env:    hostEnv("CODEX_HOME=" + home),
			}
			if name == "without account" {
				request.WithoutAccount = true
			} else {
				request.Config.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: home}}
			}
			result, err := Run(context.Background(), request)
			starts := runner.Starts()
			if name == "without account" {
				if err != nil || result.ExitCode != 0 || result.IsError || len(starts) != 1 {
					t.Fatalf("without-account result=%+v error=%v starts=%v", result, err, starts)
				}
			} else if err == nil || !strings.Contains(err.Error(), "run codex login") ||
				result.ExitCode != -1 || !result.IsError || len(starts) != 0 {
				t.Fatalf("logged-out result=%+v error=%v starts=%v", result, err, starts)
			}
		})
	}
}
