package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
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

func TestFindSessionsReportsStatFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		target  string
		wantErr bool
	}{
		{name: "loop", target: "lookup-bad.jsonl", wantErr: true},
		{name: "dangling", target: "gone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			project := filepath.Join(root, "project")
			if err := os.MkdirAll(project, 0o755); err != nil {
				t.Fatal(err)
			}
			good := filepath.Join(project, "lookup-ok.jsonl")
			if err := os.WriteFile(good, []byte("{}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(project, "lookup-bad.jsonl")
			if err := os.Symlink(tc.target, path); err != nil {
				t.Fatal(err)
			}
			found, errs := FindSessions([]string{root}, "lookup")
			if !reflect.DeepEqual(found, []string{good}) {
				t.Errorf("FindSessions = %q, want %q", found, []string{good})
			}
			if tc.wantErr {
				if len(errs) != 1 {
					t.Fatalf("errs = %v, want one Stat failure for %s", errs, path)
				}
				if !strings.Contains(errs[0].Error(), path) || !errors.Is(errs[0], syscall.ELOOP) {
					t.Errorf("error = %v, want path %s and wrapped ELOOP", errs[0], path)
				}
			} else if len(errs) != 0 {
				t.Errorf("dangling entry: errs = %v, want none", errs)
			}
		})
	}
}

func TestFindSubagentsReportsStatFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		target  string
		wantErr bool
	}{
		{name: "loop", target: "subagents", wantErr: true},
		{name: "dangling", target: "gone"},
		{name: "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := lookupFixture(t)
			projects := filepath.Join(root, "projects")
			session := filepath.Join(projects, "proj", "broken-session")
			if err := os.MkdirAll(session, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(session, "subagents")
			if tc.target != "" {
				if err := os.Symlink(tc.target, path); err != nil {
					t.Fatal(err)
				}
			}
			found, errs := FindSubagents([]string{projects}, "f00")
			base := filepath.Join(projects, "proj", "aaaa1111", "subagents")
			want := []string{
				filepath.Join(base, "agent-f00d.jsonl"),
				filepath.Join(base, "workflows", "wf_1", "agent-f00e.jsonl"),
			}
			if !reflect.DeepEqual(found, want) {
				t.Errorf("FindSubagents = %q, want %q", found, want)
			}
			if tc.wantErr {
				if len(errs) != 1 {
					t.Fatalf("errs = %v, want one Stat failure for %s", errs, path)
				}
				if !strings.Contains(errs[0].Error(), path) || !errors.Is(errs[0], syscall.ELOOP) {
					t.Errorf("error = %v, want path %s and wrapped ELOOP", errs[0], path)
				}
			} else if len(errs) != 0 {
				t.Errorf("%s subagents: errs = %v, want none", tc.name, errs)
			}
		})
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
