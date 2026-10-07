package ask

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

type fakeAskAdapter interface {
	Prepare() (AskInput, Evidence)
	WantSpanKind() string
}

// These adapters stand in for the next-wave preparation layers. They carry
// no transcript or harvester domain types: both feed only the shared ask
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

type fakeHarvesterAdapter struct{}

func (fakeHarvesterAdapter) Prepare() (AskInput, Evidence) {
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

func (fakeHarvesterAdapter) WantSpanKind() string { return "lines" }

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

func TestEvidenceStaysContentAgnosticForTranscriptAndHarvesterAdapters(t *testing.T) {
	adapters := map[string]fakeAskAdapter{
		"transcript": fakeTranscriptAdapter{},
		"harvester":  fakeHarvesterAdapter{},
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
	capture := filepath.Join(directory, "capture")
	writeAskStub(t, directory, "codex", `
printf 'home=%s\nargs=%s\n' "$CODEX_HOME" "$*" > "$ASK_CAPTURE"
IFS= read -r first
printf 'prompt=%s\n' "$first" >> "$ASK_CAPTURE"
printf '%s\n' '{"type":"thread.started"}' '{"type":"turn.started"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"codex answer"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":11,"cached_input_tokens":3,"output_tokens":5}}'`)
	writeAskStub(t, directory, "claude", `
printf 'home=%s\nargs=%s\n' "$CLAUDE_CONFIG_DIR" "$*" > "$ASK_CAPTURE"
IFS= read -r first
printf 'prompt=%s\n' "$first" >> "$ASK_CAPTURE"
printf '%s\n' '{"result":"claude answer","usage":{"input_tokens":7,"cached_input_tokens":2,"cache_creation_input_tokens":6,"output_tokens":4}}'`)
	t.Setenv("PATH", directory)
	t.Setenv("ASK_CAPTURE", capture)

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
		Claude:        pfmconfig.Claude{Binary: "claude"},
		Codex:         pfmconfig.Codex{Binary: "codex"},
	}
	tests := []struct {
		name       string
		input      AskInput
		wantHome   string
		wantArgs   []string
		wantAnswer string
		wantUsage  TokenUsage
	}{
		{
			name:     "codex",
			input:    AskInput{Engine: pfmengine.Codex, Model: "cx-model", Effort: "high"},
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
			input := test.input
			input.ContentFiles = []string{"/fixture/exchange.md"}
			input.SourceLabels = []string{"last exchange"}
			input.Prompt = "summarize"
			result, err := engine.Run(context.Background(), input)
			if err != nil {
				t.Fatalf("Run(): %v", err)
			}
			raw, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
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

func TestProcessEngineUsageIsNilWhenAbsent(t *testing.T) {
	directory := t.TempDir()
	writeAskStub(
		t,
		directory,
		"codex",
		`printf '%s\n' '{"type":"thread.started"}' '{"type":"turn.started"}' '{"type":"item.completed","item":{"type":"agent_message","text":"answer only"}}' '{"type":"turn.completed"}'`,
	)
	t.Setenv("PATH", directory)
	engine, err := ResolveEngine(pfmengine.Codex, askMachine(t, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), validAskInput("codex"))
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
			writeAskStub(t, directory, test.engine, "printf '%s\\n' '"+test.output+"'")
			t.Setenv("PATH", directory)
			id, err := pfmengine.Parse(test.engine)
			if err != nil {
				t.Fatal(err)
			}
			engine, err := ResolveEngine(id, askMachine(t, test.engine))
			if err != nil {
				t.Fatal(err)
			}
			result, err := engine.Run(context.Background(), validAskInput(test.engine))
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
	if !errors.As(err, &missing) || !strings.Contains(err.Error(), "codex binary MISSING") {
		t.Fatalf("missing error = %v", err)
	}

	writeAskStub(t, directory, "codex", "printf 'first error\\nfatal tail\\n' >&2\nexit 7")
	engine, err := ResolveEngine(pfmengine.Codex, askMachine(t, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), validAskInput("codex"))
	if err == nil || !strings.Contains(err.Error(), "exit status 7") ||
		!strings.Contains(err.Error(), `"first error\nfatal tail"`) {
		t.Fatalf("crash error = %v", err)
	}

	writeAskStub(t, directory, "codex", "/bin/sleep 5")
	engine, err = ResolveEngine(pfmengine.Codex, askMachine(t, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = engine.Run(ctx, validAskInput("codex"))
	if err == nil || !strings.Contains(err.Error(), "codex ask timed out") {
		t.Fatalf("timeout error = %v", err)
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
	return pfmconfig.Config{
		Accounts:      []pfmconfig.Account{{ID: 1, ConfigDir: configDir}},
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: codexHome}},
		Claude:        pfmconfig.Claude{Binary: "claude"},
		Codex:         pfmconfig.Codex{Binary: "codex"},
		Ask:           pfmconfig.AskConfig{Engine: id},
	}
}

func validAskInput(engineName string) AskInput {
	id, err := pfmengine.Parse(engineName)
	if err != nil {
		panic(err)
	}
	return AskInput{
		ContentFiles: []string{"/fixture/exchange.md"}, SourceLabels: []string{"last exchange"},
		Prompt: "summarize", Engine: id,
	}
}

func writeAskStub(t *testing.T, directory, name, body string) {
	t.Helper()
	if err := testjail.WriteExecutable(
		filepath.Join(directory, name),
		[]byte("#!/bin/sh\n"+body+"\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
}
