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

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
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
		"--engine claude|codex", "--system TEXT | --system-file FILE", "--sealed",
		"--allow-unsupported", "--env KEY=VALUE", "--engine-arg ARG", "--output-format text|json|native",
	} {
		if !strings.Contains(stderr.String(), phrase) {
			t.Fatalf("help omitted %q: %q", phrase, stderr.String())
		}
	}
}

func TestHeadlessExecCodexUnsupportedControlsRequireOptIn(t *testing.T) {
	headlessCLIJail(t)
	marker := filepath.Join(t.TempDir(), "started")
	binary := writeHeadlessCLIStub(
		t,
		"printf started > \""+marker+"\"\nif [ -n \"${CAPTURE_DIR:-}\" ]; then printf '%s\\n' \"$@\" > \"$CAPTURE_DIR/args\"; fi\nprintf '%s\\n' '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"ok\"}}' '{\"type\":\"turn.completed\"}'",
	)
	commandEnv := headlessCLIRuntimeFor(t, binary, pfmengine.Codex)
	baseArgs := []string{
		"--engine", "codex", "--prompt", "hello", "--output-format", "json",
		"--tools", "none", "--setting-sources", "", "--strict-mcp-config",
	}
	var stdout, stderr bytes.Buffer
	code := runHeadlessExec(baseArgs, strings.NewReader(""), &stdout, &stderr, commandEnv)
	if code != 4 || !strings.Contains(stderr.String(), "--allow-unsupported") {
		t.Fatalf("default refusal exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("default Codex capability refusal launched engine; stat err=%v", err)
	}

	capture := t.TempDir()
	commandEnv = headlessCLIRuntimeFor(t, binary, pfmengine.Codex)
	stdout.Reset()
	stderr.Reset()
	args := append(append([]string(nil), baseArgs...), "--allow-unsupported", "--env", "CAPTURE_DIR="+capture)
	code = runHeadlessExec(args, strings.NewReader(""), &stdout, &stderr, commandEnv)
	if code != 0 {
		t.Fatalf("opted-in run exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var normalized map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &normalized); err != nil {
		t.Fatalf("opted-in output is not JSON: %v (%q)", err, stdout.String())
	}
	diagnostics, ok := normalized["diagnostics"].([]any)
	if !ok || len(diagnostics) != 3 {
		t.Fatalf("opted-in diagnostics = %#v", normalized["diagnostics"])
	}
	for _, want := range []string{"--tools", "--setting-sources", "--strict-mcp-config"} {
		if cliHas(cliLines(t, filepath.Join(capture, "args")), want) {
			t.Fatalf("unsupported option reached Codex argv: %s", want)
		}
	}
	for _, want := range []string{"--tools", "--setting-sources", "--strict-mcp-config"} {
		if !strings.Contains(stderr.String(), "engine diagnostic") || !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr omitted diagnostic %s: %q", want, stderr.String())
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
		{filepath.Join(root, "doctor_harness_prompt.go"), []string{"headlessrun.Run("}, nil},
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
		{name: "codex", id: pfmengine.Codex},
	} {
		t.Run(engine.name, func(t *testing.T) {
			capture := t.TempDir()
			binary := writeHeadlessCLIStub(t, strings.Join([]string{
				"cat > \"$CAPTURE_DIR/prompt\"",
				"printf '%s\n' \"$@\" > \"$CAPTURE_DIR/args\"",
				"if [ \"$ENGINE_KIND\" = codex ]; then",
				"  printf '%s\n' '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"{\\\"ok\\\":true}\"}}' '{\"type\":\"turn.completed\"}'",
				"else",
				"  printf '%s\n' '{\"result\":\"ok\",\"structured_output\":{\"ok\":true},\"total_cost_usd\":null}'",
				"fi",
			}, "\n"))
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
				"--model", "model-x",
				"--effort", "high",
				"--timeout", "5",
				"--config-dir", cliConfigDir(commandEnv, engine.id),
				"--system-file", system,
				"--receipt", receipt,
				"--env", "CAPTURE_DIR=" + capture,
				"--env", "ENGINE_KIND=" + engine.name,
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
			} else if !cliHas(argsSeen, "model_reasoning_effort=\"high\"") {
				t.Fatalf("Codex effort mapping missing: %#v", argsSeen)
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

func cliConfigDir(commandEnv commandRuntime, engine pfmengine.ID) string {
	if engine == pfmengine.Codex {
		return commandEnv.Config.CodexAccounts[0].Home
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

func mustReadCLI(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
