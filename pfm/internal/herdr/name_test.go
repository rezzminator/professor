package herdr

import (
	"regexp"
	"strings"
	"testing"
)

var agentNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func TestAgentNameSlugsTheChatName(t *testing.T) {
	for _, test := range []struct{ name, want string }{
		{"HerDer_Ultimate_Integration", "herder_ultimate_integration"},
		{"Fix the auth bug", "fix-the-auth-bug"},
		{"  Café Ops  ", "caf-ops"},
		{"2026 plan", "c2026-plan"},
		{"日本語", "chat"},
		{strings.Repeat("a", 40), strings.Repeat("a", 32)},
		{"Review: F1 / F3 (round 2)", "review-f1-f3-round-2"},
		{"_-edge-_", "edge"},
		{strings.Repeat("a", 31) + " b", strings.Repeat("a", 31)},
	} {
		got := AgentName(test.name, nil)
		if got != test.want {
			t.Errorf("AgentName(%q) = %q, want %q", test.name, got, test.want)
		}
		if !agentNamePattern.MatchString(got) {
			t.Errorf("AgentName(%q) = %q breaks Herdr's agent-name pattern", test.name, got)
		}
	}
}

func TestAgentNameAppendsTheShortestFreeSuffix(t *testing.T) {
	long := strings.Repeat("b", 32)
	for _, test := range []struct {
		name  string
		taken map[string]bool
		want  string
	}{
		{"Fix the auth bug", map[string]bool{"fix-the-auth-bug": true}, "fix-the-auth-bug-2"},
		{"Fix the auth bug", map[string]bool{"fix-the-auth-bug": true, "fix-the-auth-bug-2": true}, "fix-the-auth-bug-3"},
		{long, map[string]bool{long: true}, strings.Repeat("b", 30) + "-2"},
		{"Fix the auth bug", map[string]bool{"other": true}, "fix-the-auth-bug"},
		{strings.Repeat("a", 30) + "_x", map[string]bool{strings.Repeat("a", 30) + "_x": true}, strings.Repeat("a", 30) + "-2"},
	} {
		got := AgentName(test.name, test.taken)
		if got != test.want {
			t.Errorf("AgentName(%q, %v) = %q, want %q", test.name, test.taken, got, test.want)
		}
	}
}
