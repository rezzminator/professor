package main

import (
	"strings"
	"testing"

	"hostops/pfm/internal/compose"
)

func TestProfessorUpdatePromptExplainsThenAsksBeforeUpdating(t *testing.T) {
	prompt := professorUpdatePrompt(compose.Row{ID: "pfm-update-v0.61.2"})
	overview := strings.Index(prompt, "present a concise overview")
	approval := strings.Index(prompt, "Ask the user for explicit approval")
	update := strings.Index(prompt, "pfm update --to v0.61.2")
	if overview < 0 || approval < overview || update < approval {
		t.Fatalf("update prompt order is not overview → approval → update: %q", prompt)
	}
	// A banner can fire for an adopter several releases behind: reading only
	// the target's notes skips every intervening release's migration actions.
	for _, want := range []string{
		"pfm doctor", "Do not push, tag, publish, release", "Professor v0.61.2",
		"pfm version", "EVERY release-notes file after the installed version through v0.61.2",
		"git show v0.61.2:releases/vX.Y.Z.md", "#### → For:", "one checklist",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("update prompt %q lacks %q", prompt, want)
		}
	}
}
