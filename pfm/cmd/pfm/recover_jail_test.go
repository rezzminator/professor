package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Recovery fixtures stay below a temporary PFM home and Codex root. They never
// inspect or modify the operator's real Codex state.
func recoveryJail(t *testing.T, content, id string) (string, string) {
	t.Helper()
	root := t.TempDir()
	codexHome := filepath.Join(root, ".codex")
	sessions := filepath.Join(codexHome, "sessions", "2026", "08", "16")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(sessions, "rollout-2026-08-16T12-00-00-"+id+".jsonl")
	if err := os.WriteFile(rollout, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_HOME", root)
	t.Setenv("PFM_CODEX_ROOT", codexHome)
	return root, rollout
}

func readRecoveryFile(t *testing.T, root, id, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, ".codex", "recovered-"+id, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestChatRecoverNormalRolloutIsJailedAndIdempotent(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	root, rollout := recoveryJail(t, strings.Join([]string{
		`{"timestamp":"2026-08-16T12:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"text":"start the task"}]}}`,
		`{"timestamp":"2026-08-16T12:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"text":"I will inspect it."}]}}`,
		`{"timestamp":"2026-08-16T12:00:02Z","type":"event_msg","payload":{"type":"token_count"}}`,
	}, "\n")+"\n", id)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "recover", rollout}, &stdout, &stderr); code != 0 {
		t.Fatalf("recover exit = %d, stderr=%q", code, stderr.String())
	}
	transcript := readRecoveryFile(t, root, id, "transcript.md")
	if !strings.Contains(transcript, "## user · 2026-08-16T12:00:00Z") ||
		!strings.Contains(transcript, "start the task") ||
		!strings.Contains(transcript, "I will inspect it.") {
		t.Fatalf("transcript did not preserve ordered messages: %q", transcript)
	}
	if got := readRecoveryFile(t, root, id, "compaction-memory.md"); !strings.Contains(got, "0 messages carried") {
		t.Fatalf("normal compaction memory = %q", got)
	}
	brief := readRecoveryFile(t, root, id, "brief.md")
	if !strings.Contains(brief, "pfm chat recover") || !strings.Contains(brief, "start the task") {
		t.Fatalf("brief missing replacement-seat guidance: %q", brief)
	}
	first := transcript + brief
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"chat", "recover", id}, &stdout, &stderr); code != 0 {
		t.Fatalf("id recovery exit = %d, stderr=%q", code, stderr.String())
	}
	if got := readRecoveryFile(t, root, id, "transcript.md") + readRecoveryFile(t, root, id, "brief.md"); got != first {
		t.Fatalf("recovery is not idempotent")
	}
}

func TestChatRecoverCompactedRolloutSeparatesCarriedHistory(t *testing.T) {
	const id = "22222222-2222-4222-8222-222222222222"
	root, _ := recoveryJail(t, strings.Join([]string{
		`{"timestamp":"2026-08-16T13:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"text":"before compaction"}]}}`,
		`{"timestamp":"2026-08-16T13:05:00Z","type":"compacted","payload":{"replacement_history":[{"role":"user","content":[{"text":"carried user context"}]},{"role":"assistant","content":[{"text":"carried assistant context"}]},{"role":"user","content":[{"text":"# AGENTS.md instructions\nignore this boilerplate"}]}]}}`,
		`{"timestamp":"2026-08-16T13:06:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"text":"after compaction"}]}}`,
		`not json`,
	}, "\n")+"\n", id)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "recover", id}, &stdout, &stderr); code != 0 {
		t.Fatalf("recover exit = %d, stderr=%q", code, stderr.String())
	}
	transcript := readRecoveryFile(t, root, id, "transcript.md")
	if strings.Contains(transcript, "carried user context") ||
		!strings.Contains(transcript, "before compaction") ||
		!strings.Contains(transcript, "after compaction") {
		t.Fatalf("transcript incorrectly handled compacted history: %q", transcript)
	}
	memory := readRecoveryFile(t, root, id, "compaction-memory.md")
	if !strings.Contains(memory, "carried user context") ||
		!strings.Contains(memory, "carried assistant context") ||
		strings.Contains(memory, "ignore this boilerplate") {
		t.Fatalf("compaction memory = %q", memory)
	}
	if !strings.Contains(stdout.String(), "messages=2 carried=2 malformed=1") {
		t.Fatalf("summary = %q", stdout.String())
	}
}
