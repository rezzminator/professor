package inject

import (
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestPaneCommandEngineRecognizesOpenCode(t *testing.T) {
	cases := []struct {
		command  string
		binaries map[pfmengine.ID]string
		want     string
	}{
		{"opencode", nil, string(pfmengine.OpenCode)},
		{"/home/me/.local/bin/opencode", nil, string(pfmengine.OpenCode)},
		{"ocx", map[pfmengine.ID]string{pfmengine.OpenCode: "ocx"}, string(pfmengine.OpenCode)},
		{"codex", nil, string(pfmengine.Codex)},
		{"claude", nil, string(pfmengine.Claude)},
		{"2.1.47", nil, string(pfmengine.Claude)},
		{"vim", nil, ""},
	}
	for _, c := range cases {
		if got := paneCommandEngine(c.command, c.binaries); got != c.want {
			t.Errorf("paneCommandEngine(%q) = %q, want %q", c.command, got, c.want)
		}
	}
}

func TestEngineNameCoversOpenCode(t *testing.T) {
	if got := injectedEngineName(string(pfmengine.OpenCode)); got != "OpenCode" {
		t.Errorf("engineName(ox) = %q, want OpenCode", got)
	}
}

func TestAutoFileThresholdInheritsCodexBoundForOpenCode(t *testing.T) {
	engine := &Engine{options: withDefaults(Options{
		ClaudeInlineMax: 500,
		CodexInlineMax:  300,
	})}
	if got := engine.inlineThreshold(string(pfmengine.OpenCode)); got != 300 {
		t.Errorf("ox threshold = %d, want Codex's 300", got)
	}
}
