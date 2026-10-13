package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/store"
)

// TestClaudeTitleRecordsFollowTheSharedTitleRule: the index names a chat by
// transcript.TitleOf — the last title record of either kind whose own member
// is a non-empty name; an empty record or a name in the other kind's member
// names nothing (INTENDED: it used to clear the title or borrow the member).
func TestClaudeTitleRecordsFollowTheSharedTitleRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "titled.jsonl")
	content := `{"type":"custom-title","customTitle":"Desk"}` + "\n" +
		`{"type":"agent-name","agentName":"Agent"}` + "\n" +
		`{"type":"custom-title","customTitle":""}` + "\n" +
		`{"type":"custom-title","agentName":"wrong member"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file := diskFile{ID: "titled", Path: path, Size: info.Size(), MTimeNS: info.ModTime().UnixNano()}
	transcript, _, err := parseClaudeTranscriptFile(file, 0, store.Transcript{})
	if err != nil {
		t.Fatal(err)
	}
	if transcript.CustomTitle != "Agent" {
		t.Fatalf("CustomTitle = %q, want %q", transcript.CustomTitle, "Agent")
	}
}

// TestClaudePromptCountSkipsAHandoffInstallAndACompactSummary: the index counts
// what a person said. The sub-agent-compact reinstall of the chat's own
// handoff (a plain user record opening the marker) and the CLI's compaction
// summary are neither a prompt nor the chat's last prompt.
func TestClaudePromptCountSkipsAHandoffInstallAndACompactSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installed.jsonl")
	content := `{"type":"user","cwd":"/work/p","message":{"content":"[sub-agent-compact: handoff] Your context was reset. Below is the handoff you wrote."}}` + "\n" +
		`{"type":"user","cwd":"/work/p","message":{"content":"first question"}}` + "\n" +
		`{"type":"user","isCompactSummary":true,"message":{"content":"This session is being continued from a summary"}}` + "\n" +
		`{"type":"user","cwd":"/work/p","message":{"content":"[sub-agent-compact: handoff] Your context was reset. Below is the handoff you wrote."}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file := diskFile{ID: "installed", Path: path, Size: info.Size(), MTimeNS: info.ModTime().UnixNano()}
	transcript, _, err := parseClaudeTranscriptFile(file, 0, store.Transcript{})
	if err != nil {
		t.Fatal(err)
	}
	if transcript.PromptCount != 1 || transcript.FirstPrompt != "first question" ||
		transcript.LastPrompt != "first question" {
		t.Fatalf("PromptCount=%d FirstPrompt=%q LastPrompt=%q, want 1 first question first question",
			transcript.PromptCount, transcript.FirstPrompt, transcript.LastPrompt)
	}
}
