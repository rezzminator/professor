package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func headlessCLIJail(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "tmux"))
	t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))
	t.Setenv("PFM_HOME", filepath.Join(root, "home"))
}

// headlessEngineBinary is a path the engine binary resolves to and nothing ever
// runs: the engine child starts through headlessExecRunner.
func headlessEngineBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engine")
	if err := testjail.WriteExecutableNeverRun(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("write engine binary %s: %v", path, err)
	}
	return path
}

// useHeadlessEngine routes `pfm headless exec`'s engine child to a fake that
// plays output, for the rest of the test, and returns the engine binary to
// configure and the fake whose Starts() hold what the child was handed.
func useHeadlessEngine(t *testing.T, output deps.StartOutput) (string, *deps.FakeRunner) {
	t.Helper()
	runner := &deps.FakeRunner{}
	runner.ScriptStartOutput(nil, output)
	previous := headlessExecRunner
	headlessExecRunner = runner
	t.Cleanup(func() { headlessExecRunner = previous })
	return headlessEngineBinary(t), runner
}

// startedStdin is what the index-th engine child was handed on stdin.
func startedStdin(t *testing.T, runner *deps.FakeRunner, index int) string {
	t.Helper()
	starts := runner.Starts()
	if index >= len(starts) {
		t.Fatalf("engine starts = %d, want a start %d", len(starts), index)
	}
	raw, err := io.ReadAll(starts[index].Opts.Stdin)
	if err != nil {
		t.Fatalf("read engine stdin: %v", err)
	}
	return string(raw)
}

// startedArgs is the index-th engine child's arguments after the binary, one per line.
func startedArgs(t *testing.T, runner *deps.FakeRunner, index int) string {
	t.Helper()
	starts := runner.Starts()
	if index >= len(starts) {
		t.Fatalf("engine starts = %d, want a start %d", len(starts), index)
	}
	return strings.Join(starts[index].Argv[1:], "\n") + "\n"
}

// startedEnvironment is the index-th engine child's environment by name.
func startedEnvironment(t *testing.T, runner *deps.FakeRunner, index int) map[string]string {
	t.Helper()
	starts := runner.Starts()
	if index >= len(starts) {
		t.Fatalf("engine starts = %d, want a start %d", len(starts), index)
	}
	values := map[string]string{}
	for _, entry := range starts[index].Opts.Env {
		if name, value, found := strings.Cut(entry, "="); found {
			values[name] = value
		}
	}
	return values
}

func headlessCLIRuntime(t *testing.T, binary string) commandRuntime {
	return headlessCLIRuntimeFor(t, binary, pfmengine.Claude)
}

func headlessCLIRuntimeFor(t *testing.T, binary string, engine pfmengine.ID) commandRuntime {
	t.Helper()
	configDir := filepath.Join(t.TempDir(), "engine-home")
	config := pfmconfig.Config{}
	switch engine {
	case pfmengine.Codex:
		config.Codex = pfmconfig.CodexPrefs{Binary: binary}
		config.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: configDir}}
	case pfmengine.OpenCode:
		configDir = filepath.Join(t.TempDir(), "opencode")
		config.OpenCode = pfmconfig.OpenCodePrefs{Binary: binary}
		config.OpenCodeAccounts = []pfmconfig.OpenCodeAccount{{ID: 1, Home: configDir}}
	default:
		if err := os.MkdirAll(configDir, 0o700); err != nil {
			t.Fatal(err)
		}
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
	binary, _ := useHeadlessEngine(t, deps.StartOutput{
		Stdout: `{"result":"hello","usage":{"input_tokens":2,"output_tokens":1},"total_cost_usd":null}` + "\n",
	})
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

func TestHeadlessExecPFMSettingsFile(t *testing.T) {
	headlessCLIJail(t)
	binary, runner := useHeadlessEngine(t, deps.StartOutput{Stdout: `{"result":"ok"}` + "\n"})
	machine := headlessCLIRuntime(t, binary)
	path := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"theme":"t","maxSubagentSpawnDepth":4,"autoCompactWindow":250000,"helperModel":"claude-sonnet-5-5"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runHeadlessExec(
		[]string{"--engine", "claude", "--prompt", "x", "--pfm-settings", path},
		strings.NewReader(""), &stdout, &stderr, machine,
	)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	argv := startedArgs(t, runner, 0)
	want := `{"env":{"ANTHROPIC_SMALL_FAST_MODEL":"claude-sonnet-5-5","CLAUDE_CODE_AUTO_COMPACT_WINDOW":"250000","CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"1","CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH":"4"},` +
		`"outputStyle":"default","theme":"t"}`
	if !strings.Contains(argv, want) {
		t.Fatalf("argv = %s", argv)
	}
}

func TestHeadlessExecClaudeIgnoresConfiguredLaunchValues(t *testing.T) {
	headlessCLIJail(t)
	binary, runner := useHeadlessEngine(t, deps.StartOutput{Stdout: `{"result":"ok"}` + "\n"})
	machine := headlessCLIRuntime(t, binary)
	machine.Config.Claude.Theme = "from-config"
	machine.Config.Claude.MaxSubagentSpawnDepth = 9
	machine.Config.Claude.WebSearchesPerSession = 99
	machine.Config.Claude.AutoCompactWindow = 5
	machine.Config.Claude.HelperModel = "from-config"
	machine.Config.Claude.TmuxTruecolor = true
	machine.Config.Claude.Cache1H = true
	var stdout, stderr bytes.Buffer
	code := runHeadlessExec([]string{"--engine", "claude", "-p", "x"}, strings.NewReader(""), &stdout, &stderr, machine)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	got := startedArgs(t, runner, 0)
	if value, found := startedEnvironment(t, runner, 0)["CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION"]; found {
		t.Fatalf("configured web search budget reached child environment: %q", value)
	}
	if !strings.HasSuffix(
		got,
		"--settings\n{\"env\":{\"ANTHROPIC_SMALL_FAST_MODEL\":\"claude-haiku-5-5\",\"CLAUDE_CODE_AUTO_COMPACT_WINDOW\":\"100000\",\"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS\":\"1\"},\"outputStyle\":\"default\"}\n",
	) {
		t.Fatalf("configured launch values reached child: %q", got)
	}
}

func TestHeadlessExecPFMSettingsErrorsNamePathOrKey(t *testing.T) {
	headlessCLIJail(t)
	machine := headlessCLIRuntime(t, headlessEngineBinary(t))
	for _, test := range []struct{ name, body, want string }{
		{"unreadable", "", "missing.json"},
		{"unknown key", `{"mystery":true}`, "mystery"},
		{"bad type", `{"theme":4}`, "theme"},
		{"zero window", `{"autoCompactWindow":0}`, "autoCompactWindow"},
		{"bad window type", `{"autoCompactWindow":"x"}`, "autoCompactWindow"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.json")
			if test.body != "" {
				if err := os.WriteFile(path, []byte(test.body), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				path = filepath.Join(t.TempDir(), "missing.json")
			}
			var stdout, stderr bytes.Buffer
			code := runHeadlessExec(
				[]string{"--engine", "claude", "--prompt", "x", "--pfm-settings", path},
				strings.NewReader(""), &stdout, &stderr, machine,
			)
			if code != 2 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("exit=%d stderr=%q, want %q", code, stderr.String(), test.want)
			}
		})
	}
}

func TestHeadlessExecNativeStreamsStdinAndTailArgs(t *testing.T) {
	headlessCLIJail(t)
	binary, runner := useHeadlessEngine(t, deps.StartOutput{Stdout: "stream me\n"})
	commandEnv := headlessCLIRuntime(t, binary)
	var stdout, stderr bytes.Buffer
	code := runHeadlessExec([]string{
		"--engine", "claude", "--output-format", "native", "--timeout", "2", "--", "--future-flag", "value with spaces",
	}, strings.NewReader("stream me\n"), &stdout, &stderr, commandEnv)
	if code != 0 || stdout.String() != "stream me\n" || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := startedStdin(t, runner, 0); got != "stream me\n" {
		t.Fatalf("engine stdin = %q, want the streamed input", got)
	}
}

func TestHeadlessExecRejectsInvalidSchemaBeforeLaunch(t *testing.T) {
	headlessCLIJail(t)
	binary, runner := useHeadlessEngine(t, deps.StartOutput{})
	commandEnv := headlessCLIRuntime(t, binary)
	var stdout, stderr bytes.Buffer
	code := runHeadlessExec([]string{
		"--engine", "claude", "--prompt", "hello", "--json-schema", "not-json",
	}, strings.NewReader(""), &stdout, &stderr, commandEnv)
	if code != 2 || !strings.Contains(stderr.String(), "schema is not valid JSON") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if starts := len(runner.Starts()); starts != 0 {
		t.Fatalf("invalid schema launched engine %d times", starts)
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

func TestHeadlessExecTaskFileTrimsAndPromptStaysRaw(t *testing.T) {
	headlessCLIJail(t)
	binary, runner := useHeadlessEngine(t, deps.StartOutput{Stdout: `{"result":"ok","total_cost_usd":null}` + "\n"})
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
	}, strings.NewReader("ignored"), &stdout, &stderr, commandEnv)
	if code != 0 {
		t.Fatalf("task-file exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	wantFramed := "===== FILE 1: source.md =====\nsource\n===== END FILE 1 =====\n\nTASK: task from file\n"
	if got := startedStdin(t, runner, 0); got != wantFramed {
		t.Fatalf("task-file prompt = %q, want %q", got, wantFramed)
	}

	rawBinary, rawRunner := useHeadlessEngine(
		t, deps.StartOutput{Stdout: `{"result":"ok","total_cost_usd":null}` + "\n"},
	)
	rawRuntime := headlessCLIRuntime(t, rawBinary)
	stdout.Reset()
	stderr.Reset()
	code = runHeadlessExec([]string{
		"--engine", "claude", "--prompt", "raw prompt",
	}, strings.NewReader("ignored"), &stdout, &stderr, rawRuntime)
	if code != 0 {
		t.Fatalf("raw prompt exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := startedStdin(t, rawRunner, 0); got != "raw prompt" {
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
			binary, runner := useHeadlessEngine(t, deps.StartOutput{Stdout: `{"result":"ok"}` + "\n"})
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
			if starts := len(runner.Starts()); starts != 0 {
				t.Fatalf("invalid file framing launched engine %d times", starts)
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
	binary, _ := useHeadlessEngine(t, deps.StartOutput{Stdout: answer, Stderr: diagnostic, ExitCode: 7})
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

func TestHeadlessExecConcurrentCallsKeepInputsIndependent(t *testing.T) {
	headlessCLIJail(t)
	binary, runner := useHeadlessEngine(t, deps.StartOutput{Stdout: `{"result":"ok","total_cost_usd":null}` + "\n"})
	captures := map[string]string{}
	var capturesLock sync.Mutex
	var calls sync.WaitGroup
	errs := make(chan error, 2)
	for _, prompt := range []string{"first independent prompt", "second independent prompt"} {
		prompt := prompt
		calls.Add(1)
		go func() {
			defer calls.Done()
			capture := t.TempDir()
			capturesLock.Lock()
			captures[capture] = prompt
			capturesLock.Unlock()
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
		}()
	}
	calls.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if starts := len(runner.Starts()); starts != len(captures) {
		t.Fatalf("engine starts = %d, want one per call (%d)", starts, len(captures))
	}
	for index := range runner.Starts() {
		capture := startedEnvironment(t, runner, index)["CAPTURE_DIR"]
		if got := startedStdin(t, runner, index); got != captures[capture] {
			t.Fatalf("start %d for %q captured prompt %q, want %q", index, capture, got, captures[capture])
		}
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
