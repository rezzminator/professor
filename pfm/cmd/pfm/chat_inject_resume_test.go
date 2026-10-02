package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestRegisteredDaemonSessionQueriesRosterConfigDirs(t *testing.T) {
	home := t.TempDir()
	dirs := []string{filepath.Join(home, ".claude"), filepath.Join(home, ".cc", "2"), filepath.Join(home, ".cc", "3")}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	logPath := filepath.Join(home, "queries.log")
	binary := filepath.Join(home, "claude")
	const id = "c3000000-3333-4333-8333-333333333333"
	script := "#!/bin/sh\nprintf '%s\\n' \"$CLAUDE_CONFIG_DIR\" >> \"$PFM_TEST_QUERY_LOG\"\n" +
		"if [ \"$CLAUDE_CONFIG_DIR\" = \"$PFM_TEST_TARGET_DIR\" ]; then\n" +
		"  printf '[{\"sessionId\":\"" + id + "\"}]'\n" +
		"else printf '[]'; fi\n"
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_TEST_QUERY_LOG", logPath)
	t.Setenv("PFM_TEST_TARGET_DIR", dirs[2])
	machine := pfmconfig.Config{
		Claude: pfmconfig.Claude{Binary: binary},
		Accounts: []pfmconfig.Account{
			{ID: 1, ConfigDir: filepath.Join(home, "wrong-implicit"), Implicit: true},
			{ID: 2, ConfigDir: dirs[1]},
			{ID: 3, ConfigDir: dirs[2]},
			{ID: 4, ConfigDir: dirs[2]},
		},
	}
	resolved := paths.Values{Home: home, Roots: map[pfmengine.ID][]string{
		pfmengine.Claude: {filepath.Join(home, "unrelated", "projects")},
	}}
	configDir, found, err := registeredDaemonSession(context.Background(), resolved, machine, id)
	if err != nil || !found || configDir != dirs[2] {
		t.Fatalf("configDir=%q found=%t err=%v", configDir, found, err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSpace(string(raw)), "\n"); len(got) != 3 ||
		got[0] != dirs[0] || got[1] != dirs[1] || got[2] != dirs[2] {
		t.Fatalf("queried config dirs=%q", raw)
	}
}

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
