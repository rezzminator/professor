package run

import (
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
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
