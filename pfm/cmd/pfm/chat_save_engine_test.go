package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChatSaveExportsAnExplicitCodexRollout pins D5b: `pfm chat save
// <out.md> <path>` given an explicit Codex rollout must export its content
// (chat read finds the same user turn and reply in this exact file shape),
// never the silent "0 user records" empty export the hardcoded Claude parser
// produced.
func TestChatSaveExportsAnExplicitCodexRollout(t *testing.T) {
	root := t.TempDir()
	rollout := filepath.Join(root, "rollout-2024-01-01T00-00-00-thread.jsonl")
	if err := os.WriteFile(
		rollout,
		[]byte(
			`{"timestamp":"t1","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"read the report"}]}}`+"\n"+
				`{"timestamp":"t2","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the report is clean"}]}}`+"\n",
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "saved.md")
	var stdout, stderr bytes.Buffer
	code := runChatSave([]string{target, rollout}, &stdout, &stderr, nil, commandRuntime{})
	if code != 0 {
		t.Fatalf("save code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "(0 user records)") {
		t.Fatalf("save reported 0 user records for a rollout chat read can parse: stdout=%q", stdout.String())
	}
	saved, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "read the report") || !strings.Contains(string(saved), "the report is clean") {
		t.Fatalf("saved transcript missing the Codex rollout's turns: %q", string(saved))
	}
}
