package transcript

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func lookupFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, path := range []string{
		"projects/proj/aaaa1111.jsonl", "projects/proj/aaaa2222.jsonl", "projects/proj/bbbb.jsonl",
		"projects/proj/.aaaa-hidden.jsonl", "projects/proj/aaaa1111/subagents/agent-f00d.jsonl",
		"projects/proj/aaaa1111/subagents/workflows/wf_1/agent-f00e.jsonl",
		"projects/proj/aaaa1111/subagents/agent-f00d.meta.json", "elsewhere/aaaa3333.jsonl",
		"projects/.hidden/aaaa4444.jsonl",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "elsewhere"), filepath.Join(root, "projects", "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "gone"), filepath.Join(root, "projects", "dangling")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFindSessionsReadsOneLevelBelowEachRoot(t *testing.T) {
	root := lookupFixture(t)
	projects := filepath.Join(root, "projects")
	found, errs := FindSessions([]string{projects, filepath.Join(root, "missing")}, "aaaa")
	want := []string{
		filepath.Join(projects, "linked", "aaaa3333.jsonl"),
		filepath.Join(projects, "proj", "aaaa1111.jsonl"), filepath.Join(projects, "proj", "aaaa2222.jsonl"),
	}
	if !reflect.DeepEqual(found, want) || len(errs) != 0 {
		t.Errorf("FindSessions = %q, %v; want %q", found, errs, want)
	}
	if _, errs := FindSessions([]string{filepath.Join(projects, "proj", "bbbb.jsonl")}, "a"); len(errs) != 1 {
		t.Errorf("a root that is a file: errs = %v, want one", errs)
	}
}

func TestFindSubagentsWalksNestedWorkflows(t *testing.T) {
	root := lookupFixture(t)
	projects := filepath.Join(root, "projects")
	found, errs := FindSubagents([]string{projects}, "f00")
	base := filepath.Join(projects, "proj", "aaaa1111", "subagents")
	want := []string{
		filepath.Join(base, "agent-f00d.jsonl"),
		filepath.Join(base, "workflows", "wf_1", "agent-f00e.jsonl"),
	}
	if !reflect.DeepEqual(found, want) || len(errs) != 0 {
		t.Errorf("FindSubagents = %q, %v; want %q", found, errs, want)
	}
	if _, err := SubagentTranscripts(filepath.Join(base, "missing")); err == nil {
		t.Error("SubagentTranscripts over a missing directory reported no error")
	}
}
