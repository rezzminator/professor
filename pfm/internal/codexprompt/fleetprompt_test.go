package codexprompt

import (
	"os"
	"path/filepath"
	"testing"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
)

// The Codex fleet prompt is the clone's composed parts with the code-review
// step mapped to Codex's shell review.
func TestFleetPromptComposesTheCloneAndMapsTheCodeReview(t *testing.T) {
	clone := t.TempDir()
	for name, body := range map[string]string{
		"share/head.md":      "head\n",
		"codex/professor.md": "- Your last step is `/code-review low` over your own change.\n",
		"share/tail.md":      "tail\n",
	} {
		path := filepath.Join(clone, "pfm", harnessprompts.DirName, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prompt, err := FleetPrompt(clone)
	if err != nil {
		t.Fatal(err)
	}
	if want := "head\n\n- Your last step is `codex review -c model=\"gpt-6.1-sol\" -c review_model=\"gpt-6.1-sol\" -c model_reasoning_effort=\"low\" \"Review the uncommitted changes only in your task's own files (name them, space separated). Ignore every other path. Report correctness bugs only, most severe first.\"` over your own change.\n\ntail\n"; prompt != want {
		t.Fatalf("FleetPrompt = %q, want %q", prompt, want)
	}
}
