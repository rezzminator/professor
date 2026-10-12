package transcript

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubagentLayout(t *testing.T) {
	session := filepath.Join(string(filepath.Separator)+"p", "-tmp-proj", "sess-1.jsonl")
	dir := SubagentsDir(session)
	if want := filepath.Join(string(filepath.Separator)+"p", "-tmp-proj", "sess-1", "subagents"); dir != want {
		t.Fatalf("SubagentsDir = %q, want %q", dir, want)
	}
	agent := SubagentTranscript(dir, "a1")
	if agent != filepath.Join(dir, "agent-a1.jsonl") ||
		SubagentMetaPath(agent) != filepath.Join(dir, "agent-a1.meta.json") {
		t.Fatalf("SubagentTranscript/SubagentMetaPath = %q / %q", agent, SubagentMetaPath(agent))
	}
	for _, tc := range []struct {
		name, id string
		ok, meta bool
	}{
		{"agent-a1.jsonl", "a1", true, false},
		{"agent-a1.meta.json", "a1", true, true},
		{"notes.jsonl", "", false, false},
		{"agent-a1.meta.json", "", false, false},
	} {
		got, ok := SubagentID(tc.name)
		if tc.meta {
			got, ok = SubagentMetaID(tc.name)
		}
		if got != tc.id || ok != tc.ok {
			t.Fatalf("id of %q (meta=%v) = %q, %v; want %q, %v", tc.name, tc.meta, got, ok, tc.id, tc.ok)
		}
	}
	nested := filepath.Join(dir, "workflows", "wf_1", "agent-w1.jsonl")
	for path, want := range map[string]string{session: "sess-1", agent: "sess-1", nested: "sess-1"} {
		if got := TranscriptSession(path); got != want {
			t.Fatalf("TranscriptSession(%q) = %q, want %q", path, got, want)
		}
	}
	if IsSubagentTranscript(session) || !IsSubagentTranscript(nested) {
		t.Fatal("IsSubagentTranscript misread the session or a nested agent")
	}
}

func TestReadSubagentMeta(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "agent-a.meta.json")
	if err := os.WriteFile(
		good,
		[]byte(`{"agentType":"executor","description":"task 1","spawnDepth":2,"parentAgentId":"p1","extra":true}`),
		0o600,
	); err != nil {
		t.Fatalf("write: %v", err)
	}
	meta, err := ReadSubagentMeta(good)
	if err != nil ||
		meta != (SubagentMeta{AgentType: "executor", Description: "task 1", SpawnDepth: 2, ParentAgentID: "p1"}) {
		t.Fatalf("ReadSubagentMeta = %+v, %v", meta, err)
	}
	if _, err := ReadSubagentMeta(filepath.Join(dir, "gone.meta.json")); !errors.Is(err, fs.ErrNotExist) ||
		!strings.Contains(err.Error(), "read sub-agent meta") {
		t.Fatalf("missing meta error = %v", err)
	}
	bad := filepath.Join(dir, "agent-b.meta.json")
	if err := os.WriteFile(bad, []byte(`{`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ReadSubagentMeta(bad); err == nil || !strings.Contains(err.Error(), "parse sub-agent meta") {
		t.Fatalf("malformed meta error = %v", err)
	}
}

// A directory named subagents above the transcript root makes no main chat an
// agent: only an agent-<id>.jsonl inside one is.
func TestSubagentsAboveTheRootAreNotAgents(t *testing.T) {
	sep := string(filepath.Separator)
	main := filepath.Join(sep+"data", "subagents", "projects", "-w-demo", "s1.jsonl")
	if IsSubagentTranscript(main) || TranscriptSession(main) != "s1" || TranscriptStem(main) != "s1" {
		t.Fatalf("main chat under a subagents directory: sub=%v session=%q",
			IsSubagentTranscript(main), TranscriptSession(main))
	}
	agent := filepath.Join(sep+"data", "subagents", "projects", "-w-demo", "s1", "subagents", "agent-a.jsonl")
	dir, ok := SessionDir(agent)
	if !IsSubagentTranscript(agent) || TranscriptSession(agent) != "s1" || !ok ||
		dir != filepath.Join(sep+"data", "subagents", "projects", "-w-demo", "s1") {
		t.Fatalf("agent: sub=%v session=%q dir=%q", IsSubagentTranscript(agent), TranscriptSession(agent), dir)
	}
	if _, ok := SessionDir(filepath.Join(sep+"w", "s1.jsonl")); ok {
		t.Fatal("SessionDir found a subagents directory where none is")
	}
}

func TestReadSubagentMetaRefusesANonObjectMeta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-x.meta.json")
	if err := os.WriteFile(path, []byte("null\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSubagentMeta(
		path,
	); err == nil ||
		err.Error() != "parse sub-agent meta "+path+": not a JSON object" {
		t.Errorf("ReadSubagentMeta of a null meta = %v, want the not-a-JSON-object refusal", err)
	}
}
