package digest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentsListsEachSubAgentEarliestFirst(t *testing.T) {
	project := t.TempDir()
	session := filepath.Join(project, "sess.jsonl")
	folder := filepath.Join(project, "sess", "subagents")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(session, "{}\n")
	write(filepath.Join(folder, "agent-late.jsonl"), `{"type":"assistant","timestamp":"2026-09-29T20:00:30Z",`+
		`"message":{"content":[{"type":"tool_use"},{"type":"text"},{"type":"tool_use"}]}}`+"\n\n")
	write(filepath.Join(folder, "agent-late.meta.json"), `{"agentType":"gitter","description":"Commit"}`)
	write(filepath.Join(folder, "agent-early.jsonl"), `{"type":"user","timestamp":"2026-09-29T23:00:10+03:00"}`+"\n")
	write(filepath.Join(folder, "agent-none.jsonl"), "not json\n")
	text, err := sessionAgents(session)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != 5 || lines[1] != "AGENTS 3" {
		t.Fatalf("agents = %q", lines)
	}
	wantPrefix := []string{
		"early · META ERROR missing " + filepath.Join(folder, "agent-early.meta.json") +
			" · 2026-09-29 20:00:10Z → 2026-09-29 20:00:10Z · 1 records · ",
		"late · gitter · Commit · 2026-09-29 20:00:30Z → 2026-09-29 20:00:30Z · 1 records · ",
		"none · META ERROR missing " + filepath.Join(
			folder,
			"agent-none.meta.json",
		) + " · no timestamps · 1 records · ",
	}
	for index, prefix := range wantPrefix {
		if !strings.HasPrefix(lines[index+2], prefix) {
			t.Errorf("row %d = %q, want prefix %q", index, lines[index+2], prefix)
		}
	}
	if !strings.HasSuffix(lines[3], " · 2 calls") {
		t.Errorf("late row counts its tool_use blocks: %q", lines[3])
	}
	_, err = sessionAgents(filepath.Join(folder, "agent-late.jsonl"))
	var failure *Failure
	if !errors.As(err, &failure) || !failure.Usage {
		t.Errorf("agents on a sub-agent = %v, want a usage refusal", err)
	}
}

func TestAgentsRefusesASubagentsPathThatIsNoDirectory(t *testing.T) {
	project := t.TempDir()
	session := filepath.Join(project, "sess.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, "sess"), 0o755); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(project, "sess", "subagents")
	if err := os.WriteFile(folder, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := sessionAgents(session)
	var failure *Failure
	if !errors.As(err, &failure) || failure.Usage || failure.Message != "subagents path is not a directory: "+folder {
		t.Errorf("agents over a subagents file = %v, want the runtime refusal naming it", err)
	}
}
