package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
)

func TestResolveResumeTargetDeduplicatesSameSessionIDBeforeAmbiguity(t *testing.T) {
	const (
		excerpt = "the same session appears twice"
		id      = "11111111-1111-4111-8111-111111111111"
	)
	root := t.TempDir()
	first := filepath.Join(root, "a", "projects")
	second := filepath.Join(root, "z", "projects")
	writeResumeTranscript(t, first, id, excerpt)
	writeResumeTranscript(t, second, id, excerpt)

	target, found, err := resolveResumeTarget(excerpt, resumeTranscriptRuntime(root, first, second))
	if err != nil {
		t.Fatalf("resolveResumeTarget() error = %v, want one session after duplicate-ID dedupe", err)
	}
	if !found {
		t.Fatal("resolveResumeTarget() found=false, want the duplicated session")
	}
	wantPath := filepath.Join(first, "project", id+".jsonl")
	if target.ID != id || target.Path != wantPath {
		t.Fatalf("resolveResumeTarget() = %+v, want ID %q and deterministic first path %q", target, id, wantPath)
	}
}

func TestResolveResumeTargetKeepsDistinctEqualHitsAmbiguous(t *testing.T) {
	const excerpt = "two distinct sessions say this"
	root := t.TempDir()
	first := filepath.Join(root, "a", "projects")
	second := filepath.Join(root, "z", "projects")
	writeResumeTranscript(t, first, "22222222-2222-4222-8222-222222222222", excerpt)
	writeResumeTranscript(t, second, "33333333-3333-4333-8333-333333333333", excerpt)

	_, found, err := resolveResumeTarget(excerpt, resumeTranscriptRuntime(root, first, second))
	if found {
		t.Fatal("resolveResumeTarget() found=true, want distinct equal-hit sessions to remain ambiguous")
	}
	if err == nil || !strings.Contains(err.Error(), "excerpt is ambiguous between") {
		t.Fatalf("resolveResumeTarget() error = %v, want named distinct-session ambiguity", err)
	}
}

func resumeTranscriptRuntime(home string, roots ...string) commandRuntime {
	return commandRuntime{Paths: paths.Values{
		Home:  home,
		Roots: map[pfmengine.ID][]string{pfmengine.Claude: roots},
	}}
}

func writeResumeTranscript(t *testing.T, root, id, excerpt string) {
	t.Helper()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","message":{"content":"` + excerpt + `"}}
`
	if err := os.WriteFile(filepath.Join(project, id+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
