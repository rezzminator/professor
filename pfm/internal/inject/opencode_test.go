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

func TestTargetFromPartsRecognizesOpenCodeSockets(t *testing.T) {
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "")
	for _, socket := range []string{"/tmp/tmux-0/ox-1-2-3,99,0"} {
		target := targetFromParts(socket, "%5")
		if target.Engine != string(pfmengine.OpenCode) {
			t.Errorf("targetFromParts(%q).Engine = %q, want ox", socket, target.Engine)
		}
	}
	if got := targetFromParts("/tmp/tmux-0/cc-9,1,0", "%1").Engine; got != "cc" {
		t.Errorf("cc socket misread as %q", got)
	}
}

func TestTargetFromPartsProbeOpenCodeSocket(t *testing.T) {
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "1")
	target := targetFromParts("/tmp/jail/probe-ox-7,3,0", "%2")
	if target.Engine != string(pfmengine.OpenCode) {
		t.Errorf("probe socket engine = %q, want ox", target.Engine)
	}
}

func TestTargetFromPartsNamesUnknownSocketEngine(t *testing.T) {
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "")
	target := targetFromParts("/tmp/tmux-0/unmanaged,1,0", "%2")
	if target.Engine != "unknown" {
		t.Fatalf("unknown socket engine=%q, want an explicit unknown label", target.Engine)
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
