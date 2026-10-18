package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func selectorConfig() pfmconfig.Config {
	return pfmconfig.Config{
		Accounts:         []pfmconfig.Account{{ID: 1, ConfigDir: "/claude"}},
		CodexAccounts:    []pfmconfig.CodexAccount{{ID: 1, Home: "/codex"}},
		OpenCodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 1, Home: "/opencode"}},
		Ask: pfmconfig.AskConfig{
			Engine: pfmengine.Codex,
			Prefs: map[pfmengine.ID]pfmconfig.EnginePrefs{
				pfmengine.Codex:    {Model: "codex-model", Effort: "high"},
				pfmengine.OpenCode: {Model: "opencode-model", Effort: "medium"},
				pfmengine.Claude:   {Model: "claude-model", Effort: "low"},
			},
		},
	}
}

func TestApplyEngineSelectorRoutesCodexThroughOpenCodeKeepingItsPreferences(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		selector   string
		model      string
		wantEngine pfmengine.ID
		wantModel  string
		wantEffort string
	}{
		{name: "codex", selector: "codex", wantEngine: pfmengine.OpenCode, wantModel: "codex-model", wantEffort: "high"},
		{name: "cx alias", selector: "cx", wantEngine: pfmengine.OpenCode, wantModel: "codex-model", wantEffort: "high"},
		{name: "opencode", selector: "opencode", wantEngine: pfmengine.OpenCode},
		{name: "ox alias", selector: "ox", wantEngine: pfmengine.OpenCode},
		{name: "claude", selector: "claude", wantEngine: pfmengine.Claude},
		{name: "configured default", selector: "", wantEngine: pfmengine.OpenCode, wantModel: "codex-model", wantEffort: "high"},
		{
			name: "explicit model wins", selector: "codex", model: "chosen",
			wantEngine: pfmengine.OpenCode, wantModel: "chosen", wantEffort: "high",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := Request{Config: selectorConfig(), Model: testCase.model}
			if err := ApplyEngineSelector(&request, testCase.selector); err != nil {
				t.Fatalf("ApplyEngineSelector(%q) error = %v", testCase.selector, err)
			}
			if request.Engine != testCase.wantEngine {
				t.Fatalf("engine = %q, want %q", request.Engine, testCase.wantEngine)
			}
			if request.Model != testCase.wantModel || request.Effort != testCase.wantEffort {
				t.Fatalf(
					"model/effort = %q/%q, want %q/%q",
					request.Model, request.Effort, testCase.wantModel, testCase.wantEffort,
				)
			}
		})
	}
}

func TestApplyEngineSelectorReportsAnUnknownSelectorAndAnEmptyRoster(t *testing.T) {
	request := Request{Config: selectorConfig()}
	if err := ApplyEngineSelector(&request, "gemini"); err == nil {
		t.Fatal("unknown selector was accepted")
	}
	empty := Request{}
	err := ApplyEngineSelector(&empty, "")
	if err == nil || !strings.Contains(err.Error(), "no engines configured") {
		t.Fatalf("empty roster default error = %v, want the configured-engines refusal", err)
	}
}

// `pfm headless --engine cx --model astra` runs on OpenCode: the bare tier resolves to the astra
// persona's Codex id before the switch, so OpenCode is handed openai/gpt-6-astra, never astra.
func TestApplyEngineSelectorResolvesBareCodexTier(t *testing.T) {
	home := t.TempDir()
	agents := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	persona := "---\nname: astra\nmodel: gpt-6-astra[1m]\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(agents, "astra.md"), []byte(persona), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvHome, home)
	request := Request{Config: selectorConfig(), Model: "astra"}
	if err := ApplyEngineSelector(&request, "cx"); err != nil {
		t.Fatal(err)
	}
	if request.Model != "gpt-6-astra" || openCodeModel(request.Model) != "openai/gpt-6-astra" {
		t.Fatalf("--engine cx --model astra resolved to %q", request.Model)
	}
	args, err := codexExecModelArgs("astra")
	if err != nil || strings.Join(args, " ") != "--model gpt-6-astra" {
		t.Fatalf("codex exec model args = %q, %v", args, err)
	}
}
