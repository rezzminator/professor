package ask

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
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
		resolved, err := ResolveInput(input, askMachine("codex"))
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
	writeAskStub(t, directory, "codex", `
printf 'home=%s\n' "$CODEX_HOME"
printf 'args=%s\n' "$*"
IFS= read -r first
printf 'prompt=%s\n' "$first"
printf 'usage: input_tokens=11 cached_input_tokens=3 output_tokens=5\n'
printf 'codex answer\n'`)
	writeAskStub(t, directory, "claude", `
printf 'home=%s\n' "$CLAUDE_CONFIG_DIR"
printf 'args=%s\n' "$*"
IFS= read -r first
printf 'prompt=%s\n' "$first"
printf 'usage: input_tokens=7 cached_input_tokens=2 output_tokens=4\n'
printf 'claude answer\n'`)
	t.Setenv("PATH", directory)

	machine := pfmconfig.Config{
		Accounts:      []pfmconfig.Account{{ID: 2, ConfigDir: "/fixture/claude-2"}},
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 4, Home: "/fixture/codex-4"}},
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
			wantHome: "/fixture/codex-4",
			wantArgs: []string{
				"exec",
				"--model cx-model",
				`model_reasoning_effort="high"`,
				"--ephemeral",
				"--skip-git-repo-check",
				"-",
			},
			wantAnswer: "codex answer",
			wantUsage:  TokenUsage{Input: 11, CachedInput: 3, Output: 5},
		},
		{
			name:       "claude",
			input:      AskInput{Engine: pfmengine.Claude, Model: "cc-model", Effort: "medium"},
			wantHome:   "/fixture/claude-2",
			wantArgs:   []string{"-p", "--model cc-model", "--effort medium", "--output-format text"},
			wantAnswer: "claude answer",
			wantUsage:  TokenUsage{Input: 7, CachedInput: 2, Output: 4},
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
			for _, want := range append([]string{"home=" + test.wantHome, "prompt=Read the prepared content files"}, test.wantArgs...) {
				if !strings.Contains(result.Answer, want) {
					t.Errorf("answer %q does not contain %q", result.Answer, want)
				}
			}
			if strings.Contains(result.Answer, "usage:") || !strings.Contains(result.Answer, test.wantAnswer) {
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
	writeAskStub(t, directory, "codex", "printf 'answer only\\n'")
	t.Setenv("PATH", directory)
	engine, err := ResolveEngine(pfmengine.Codex, askMachine("codex"))
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

func TestProcessEngineDistinguishesMissingCrashAndTimeout(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("PATH", directory)
	machine := askMachine("codex")
	machine.Codex.Binary = "missing-codex"
	_, err := ResolveEngine(pfmengine.Codex, machine)
	var missing *BinaryMissingError
	if !errors.As(err, &missing) || !strings.Contains(err.Error(), "codex binary MISSING") {
		t.Fatalf("missing error = %v", err)
	}

	writeAskStub(t, directory, "codex", "printf 'first error\\nfatal tail\\n' >&2\nexit 7")
	engine, err := ResolveEngine(pfmengine.Codex, askMachine("codex"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), validAskInput("codex"))
	if err == nil || !strings.Contains(err.Error(), "exit status 7") ||
		!strings.Contains(err.Error(), `"first error\nfatal tail"`) {
		t.Fatalf("crash error = %v", err)
	}

	writeAskStub(t, directory, "codex", "/bin/sleep 5")
	engine, err = ResolveEngine(pfmengine.Codex, askMachine("codex"))
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

func askMachine(engineName string) pfmconfig.Config {
	id, err := pfmengine.Parse(engineName)
	if err != nil {
		panic(err)
	}
	return pfmconfig.Config{
		Accounts:      []pfmconfig.Account{{ID: 1, ConfigDir: "/fixture/claude"}},
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: "/fixture/codex"}},
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
	if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}
