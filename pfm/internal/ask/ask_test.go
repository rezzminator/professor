package ask

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

type fakeAskAdapter interface {
	Prepare() (AskInput, Evidence)
	WantSpanKind() string
}

// These adapters stand in for the next-wave preparation layers. They carry
// no transcript or document domain types: both feed only the shared ask
// contract and preserve provenance in its generic fields.
type fakeTranscriptAdapter struct{}

func (fakeTranscriptAdapter) Prepare() (AskInput, Evidence) {
	input := AskInput{
		ContentFiles: []string{"prepared-transcript.md"},
		SourceLabels: []string{"session fixture#turns 1-14"},
		Prompt:       "find the visible answer",
	}
	evidence := Evidence{
		File:  "prepared-transcript.md",
		Label: "session fixture#turns 1-14",
		Span:  SourceSpan{Kind: "turns", Start: 1, End: 14},
		Quote: "visible answer",
	}
	return input, evidence
}

func (fakeTranscriptAdapter) WantSpanKind() string { return "turns" }

type fakeDocumentAdapter struct{}

func (fakeDocumentAdapter) Prepare() (AskInput, Evidence) {
	input := AskInput{
		ContentFiles: []string{"prepared-source.md"},
		SourceLabels: []string{"https://fixture.invalid/source"},
		Prompt:       "find the source claim",
	}
	evidence := Evidence{
		File:  "prepared-source.md",
		Label: "https://fixture.invalid/source",
		Span:  SourceSpan{Kind: "lines", Start: 4, End: 9},
		Quote: "source claim",
	}
	return input, evidence
}

func (fakeDocumentAdapter) WantSpanKind() string { return "lines" }

func TestResolveInputUsesExplicitValuesBeforeConfig(t *testing.T) {
	cfg := pfmconfig.Config{Ask: pfmconfig.AskConfig{
		Engine: pfmengine.Claude,
		Prefs: map[pfmengine.ID]pfmconfig.EnginePrefs{
			pfmengine.Codex:  {Model: "codex-default", Effort: "medium"},
			pfmengine.Claude: {Model: "claude-default", Effort: "low"},
		},
	}, Accounts: []pfmconfig.Account{{ID: 1}}}
	resolved, err := ResolveInput(AskInput{
		ContentFiles: []string{"prepared.md"}, SourceLabels: []string{"fixture"}, Prompt: "answer",
		Engine: pfmengine.Codex, Model: "custom-model",
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Engine != pfmengine.Codex || resolved.Model != "custom-model" || resolved.Effort != "medium" {
		t.Fatalf("resolved = %+v", resolved)
	}
	resolved, err = ResolveInput(AskInput{ContentFiles: []string{"prepared.md"}, Prompt: "answer"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Engine != pfmengine.Claude || resolved.Model != "claude-default" || resolved.Effort != "low" {
		t.Fatalf("config resolution = %+v", resolved)
	}
}

func TestEvidenceStaysContentAgnosticForTranscriptAndDocumentAdapters(t *testing.T) {
	adapters := map[string]fakeAskAdapter{
		"transcript": fakeTranscriptAdapter{},
		"document":   fakeDocumentAdapter{},
	}
	for name, adapter := range adapters {
		input, evidence := adapter.Prepare()
		resolved, err := ResolveInput(input, askMachine(t, "codex"))
		if err != nil {
			t.Fatalf("%s adapter ResolveInput(): %v", name, err)
		}
		if _, err := BuildPrompt(resolved); err != nil {
			t.Fatalf("%s adapter BuildPrompt(): %v", name, err)
		}
		if evidence.File != resolved.ContentFiles[0] || evidence.Label != resolved.SourceLabels[0] {
			t.Fatalf("%s adapter evidence=%+v does not feed resolved input=%+v", name, evidence, resolved)
		}
		if evidence.Span.Kind != adapter.WantSpanKind() {
			t.Fatalf("%s adapter span kind=%q, want %q", name, evidence.Span.Kind, adapter.WantSpanKind())
		}
	}
}

func TestProcessEnginesUseRosterHomesConfigAndPrompt(t *testing.T) {
	directory := t.TempDir()
	writeAskBinary(t, directory, "codex")
	writeAskBinary(t, directory, "claude")
	t.Setenv("PATH", directory)

	configDir := filepath.Join(directory, "claude-2")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	codexHome := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(codexHome, "auth.json"),
		[]byte(`{"tokens":{"access_token":"fixture","account_id":"fixture"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	machine := pfmconfig.Config{
		Accounts:      []pfmconfig.Account{{ID: 2, ConfigDir: configDir}},
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 4, Home: codexHome}},
		Claude:        pfmconfig.Claude{Binary: filepath.Join(directory, "claude")},
		Codex:         pfmconfig.Codex{Binary: filepath.Join(directory, "codex")},
	}
	tests := []struct {
		name       string
		input      AskInput
		homeEnv    string
		stdout     string
		wantHome   string
		wantArgs   []string
		wantAnswer string
		wantUsage  TokenUsage
	}{
		{
			name:    "codex",
			input:   AskInput{Engine: pfmengine.Codex, Model: "cx-model", Effort: "high"},
			homeEnv: "CODEX_HOME",
			stdout: `{"type":"thread.started"}
{"type":"turn.started"}
{"type":"item.completed","item":{"type":"agent_message","text":"codex answer"}}
{"type":"turn.completed","usage":{"input_tokens":11,"cached_input_tokens":3,"output_tokens":5}}
`,
			wantHome: codexHome,
			wantArgs: []string{
				"exec",
				"--model cx-model",
				`model_reasoning_effort="high"`,
				"--json",
				"--ephemeral",
				"--skip-git-repo-check",
				"--color never",
			},
			wantAnswer: "codex answer",
			wantUsage:  TokenUsage{Input: 11, CachedInput: 3, Output: 5},
		},
		{
			name:       "claude",
			input:      AskInput{Engine: pfmengine.Claude, Model: "cc-model", Effort: "medium"},
			homeEnv:    "CLAUDE_CONFIG_DIR",
			stdout:     `{"result":"claude answer","usage":{"input_tokens":7,"cached_input_tokens":2,"cache_creation_input_tokens":6,"output_tokens":4}}` + "\n",
			wantHome:   configDir,
			wantArgs:   []string{"-p", "--model cc-model", "--effort medium", "--output-format json"},
			wantAnswer: "claude answer",
			wantUsage:  TokenUsage{Input: 7, CachedInput: 2, CacheCreation: 6, Output: 4},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			id, parseErr := pfmengine.Parse(test.name)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			engine, err := ResolveEngine(id, machine)
			if err != nil {
				t.Fatalf("ResolveEngine(): %v", err)
			}
			var capture string
			runner := &deps.FakeRunner{}
			runner.ScriptStartOutput(
				nil,
				deps.StartOutput{AtStart: recordAsk(&capture, test.homeEnv), Stdout: test.stdout},
			)
			engine = WithRunner(engine, runner)
			input := test.input
			input.ContentFiles = validAskInput(t, test.name).ContentFiles
			input.SourceLabels = []string{"last exchange"}
			input.Prompt = "summarize"
			result, err := engine.Run(context.Background(), input)
			if err != nil {
				t.Fatalf("Run(): %v", err)
			}
			raw := capture
			for _, want := range append([]string{"home=" + test.wantHome, "prompt=Read the prepared content files"}, test.wantArgs...) {
				if !strings.Contains(string(raw), want) {
					t.Errorf("capture %q does not contain %q", raw, want)
				}
			}
			if result.Answer != test.wantAnswer {
				t.Errorf("answer = %q", result.Answer)
			}
			if result.Usage == nil || *result.Usage != test.wantUsage {
				t.Fatalf("usage = %#v, want %#v", result.Usage, test.wantUsage)
			}
		})
	}
}

func TestProcessEngineLeavesDocumentContentOnDisk(t *testing.T) {
	directory := t.TempDir()
	writeAskBinary(t, directory, "codex")
	t.Setenv("PATH", directory)
	engine, err := ResolveEngine(pfmengine.Codex, askMachine(t, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	runner := &deps.FakeRunner{}
	runner.ScriptStartOutput(nil, deps.StartOutput{
		AtStart: func(_ []string, opts deps.StartOptions) { raw, _ = io.ReadAll(opts.Stdin) },
		Stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}
{"type":"turn.completed"}
`,
	})
	engine = WithRunner(engine, runner)
	input := validAskInput(t, "codex")
	if _, err := engine.Run(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.ReplaceAll(
		string(raw),
		filepath.Dir(input.ContentFiles[0]),
		"{dir}",
	), "Read the prepared content files listed below. Work ONLY from them; no network access, no other files.\n1. {dir}/exchange.md — source: last exchange\nTASK: summarize\nRules: if a file is truncated or unusable, say so explicitly for that file instead of guessing.\nAfter your answer, append a section titled exactly \"EVIDENCE\" listing one line per load-bearing claim:\n  [file N] <location: line range, turn number, or chunk id> — \"<short verbatim quote>\""; got != want {
		t.Fatalf("document ask stdin = %q, want %q", got, want)
	}
}

func TestProcessEngineUsageIsNilWhenAbsent(t *testing.T) {
	directory := t.TempDir()
	writeAskBinary(t, directory, "codex")
	t.Setenv("PATH", directory)
	engine, err := ResolveEngine(pfmengine.Codex, askMachine(t, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	engine = WithRunner(engine, askChild(deps.StartOutput{Stdout: `{"type":"thread.started"}
{"type":"turn.started"}
{"type":"item.completed","item":{"type":"agent_message","text":"answer only"}}
{"type":"turn.completed"}
`}))
	result, err := engine.Run(context.Background(), validAskInput(t, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage != nil {
		t.Fatalf("usage = %#v, want nil", result.Usage)
	}
}

func TestProcessEngineTakesAnswerAndErrorsFromJSON(t *testing.T) {
	for _, test := range []struct {
		name, engine, output, wantAnswer, wantError string
	}{
		{"quoted count", "claude", `{"result":"input_tokens: 5","usage":{"input_tokens":12}}`, "input_tokens: 5", ""},
		{"Claude error", "claude", `{"result":"failed","is_error":true}`, "", "claude ask failed"},
		{"Codex failure", "codex", `{"type":"turn.failed","error":{"message":"failed"}}`, "", "codex ask failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writeAskBinary(t, directory, test.engine)
			t.Setenv("PATH", directory)
			id, err := pfmengine.Parse(test.engine)
			if err != nil {
				t.Fatal(err)
			}
			engine, err := ResolveEngine(id, askMachine(t, test.engine))
			if err != nil {
				t.Fatal(err)
			}
			engine = WithRunner(engine, askChild(deps.StartOutput{Stdout: test.output + "\n"}))
			result, err := engine.Run(context.Background(), validAskInput(t, test.engine))
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error=%v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil || result.Answer != test.wantAnswer || result.Usage == nil || result.Usage.Input != 12 {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
}

func TestProcessEngineDistinguishesMissingCrashAndTimeout(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("PATH", directory)
	machine := askMachine(t, "codex")
	machine.Codex.Binary = "missing-codex"
	_, err := ResolveEngine(pfmengine.Codex, machine)
	var missing *BinaryMissingError
	if got, want := strings.ReplaceAll(
		fmt.Sprint(err),
		directory,
		"{dir}",
	), "codex binary MISSING (missing-codex)"; !errors.As(err, &missing) ||
		got != want {
		t.Fatalf("missing error = %q, want %q", got, want)
	}

	writeAskBinary(t, directory, "codex")
	engine, err := ResolveEngine(pfmengine.Codex, askMachine(t, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	engine = WithRunner(engine, askChild(deps.StartOutput{Stderr: "first error\nfatal tail\n", ExitCode: 7}))
	_, err = engine.Run(context.Background(), validAskInput(t, "codex"))
	if got, want := strings.ReplaceAll(
		fmt.Sprint(err),
		directory,
		"{dir}",
	), "codex ask failed: cx headless run failed: exit status 7; stderr tail \"first error\\nfatal tail\"; stdout tail \"\""; got != want {
		t.Fatalf("crash error = %q, want %q", got, want)
	}

	engine, err = ResolveEngine(pfmengine.Codex, askMachine(t, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	engine = WithRunner(engine, askChild(deps.StartOutput{}))
	// The deadline has passed when the child "starts": the fake child plays no time of its own.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	started := time.Now()
	_, err = engine.Run(ctx, validAskInput(t, "codex"))
	if got, want := strings.ReplaceAll(
		fmt.Sprint(err),
		directory,
		"{dir}",
	), "codex ask timed out: context deadline exceeded"; got != want {
		t.Fatalf("timeout error = %q, want %q", got, want)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timeout returned after %s, want under 1s", elapsed)
	}
}

func askMachine(t *testing.T, engineName string) pfmconfig.Config {
	t.Helper()
	configDir := filepath.Join(t.TempDir(), "claude")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	codexHome := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(codexHome, "auth.json"),
		[]byte(`{"tokens":{"access_token":"fixture","account_id":"fixture"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	id, err := pfmengine.Parse(engineName)
	if err != nil {
		panic(err)
	}
	binaries := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		if err := testjail.WriteExecutableNeverRun(
			filepath.Join(binaries, name),
			[]byte("#!/bin/sh\n"),
			0o700,
		); err != nil {
			t.Fatal(err)
		}
	}
	return pfmconfig.Config{
		Accounts:      []pfmconfig.Account{{ID: 1, ConfigDir: configDir}},
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: codexHome}},
		Claude:        pfmconfig.Claude{Binary: filepath.Join(binaries, "claude")},
		Codex:         pfmconfig.Codex{Binary: filepath.Join(binaries, "codex")},
		Ask:           pfmconfig.AskConfig{Engine: id},
	}
}

func validAskInput(t *testing.T, engineName string) AskInput {
	t.Helper()
	file := filepath.Join(t.TempDir(), "exchange.md")
	if err := os.WriteFile(file, []byte("prepared exchange"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := pfmengine.Parse(engineName)
	if err != nil {
		panic(err)
	}
	return AskInput{
		ContentFiles: []string{file}, SourceLabels: []string{"last exchange"},
		Prompt: "summarize", Engine: id,
	}
}

// writeAskBinary writes an engine binary the code resolves and never runs: the
// child starts through the WithRunner fake.
func writeAskBinary(t *testing.T, directory, name string) {
	t.Helper()
	if err := testjail.WriteExecutableNeverRun(
		filepath.Join(directory, name),
		[]byte("#!/bin/sh\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
}

// askChild is the runner of a test: the engine child plays output and starts nowhere.
func askChild(output deps.StartOutput) *deps.FakeRunner {
	runner := &deps.FakeRunner{}
	runner.ScriptStartOutput(nil, output)
	return runner
}

// recordAsk captures what the engine child was given: the value of its home
// variable, its arguments and the first line of its stdin.
func recordAsk(capture *string, homeVariable string) func([]string, deps.StartOptions) {
	return func(argv []string, opts deps.StartOptions) {
		first, _ := bufio.NewReader(opts.Stdin).ReadString('\n')
		home := ""
		for _, entry := range opts.Env {
			if value, found := strings.CutPrefix(entry, homeVariable+"="); found {
				home = value
			}
		}
		*capture = fmt.Sprintf(
			"home=%s\nargs=%s\nprompt=%s\n",
			home,
			strings.Join(argv[1:], " "),
			strings.TrimSuffix(first, "\n"),
		)
	}
}

// TestCheckModelRefusesAModelItsEngineCannotRunByName: a Claude Code alias
// (in any case, with or without [1m]) or a price-table row of the engine
// passes as the model to launch; any other model is refused naming the model
// and the engine, before an engine starts.
func TestCheckModelRefusesAModelItsEngineCannotRunByName(t *testing.T) {
	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	refusedClaude := ` is not a claude model: name an alias (fable, opus, sonnet, haiku) or a model id the price table lists`
	for _, testCase := range []struct {
		engine pfmengine.ID
		model  string
		run    string // the model to launch; "" = refused with want
		want   string
	}{
		{pfmengine.Claude, "haiku", "haiku", ""},
		{pfmengine.Claude, " Sonnet ", "sonnet", ""},
		{pfmengine.Claude, "sonnet[1m]", "sonnet[1m]", ""},
		{pfmengine.Claude, "claude-haiku-4-5", "claude-haiku-4-5", ""},
		{pfmengine.Codex, "gpt-5", "gpt-5", ""},
		{pfmengine.Claude, "gpt-5", "", `model "gpt-5"` + refusedClaude},
		{pfmengine.Claude, "claude-nonexistent-9", "", `model "claude-nonexistent-9"` + refusedClaude},
		{pfmengine.Claude, "haiku[2m]", "", `model "haiku[2m]"` + refusedClaude},
		{pfmengine.Codex, "haiku", "", `model "haiku" is not a codex model: name a model id the price table lists`},
	} {
		run, err := CheckModel(testCase.engine, testCase.model, table)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if run != testCase.run || got != testCase.want {
			t.Errorf("CheckModel(%s, %q) = %q, %q, want %q, %q",
				testCase.engine, testCase.model, run, got, testCase.run, testCase.want)
		}
	}
}
