package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestRegisteredDaemonSessionQueriesRosterConfigDirs(t *testing.T) {
	home := t.TempDir()
	dirs := []string{
		pfmconfig.DefaultAccountDir(home, 1),
		filepath.Join(home, ".cc", "2"),
		filepath.Join(home, ".cc", "3"),
	}
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
			{ID: 1, ConfigDir: dirs[0]},
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

// A dormant chat's append keeps its parent past a torn line and takes the
// session id from the transcript's name when the tail event carries none.
func TestAppendResumeCallKeepsTheTailItCanRead(t *testing.T) {
	const id = "c4000000-4444-4444-8444-444444444444"
	const parent = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	for _, testCase := range []struct {
		name, transcript string
	}{
		{
			name: "torn line before the tail",
			transcript: `{"type":"user","uuid":"` + parent + `","sessionId":"` + id + `"}` + "\n" +
				`{"type":"assistant","uuid":` + "\n",
		},
		{
			name:       "tail without a session id",
			transcript: `{"type":"user","uuid":"` + parent + `"}` + "\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(t.TempDir(), id+".jsonl")
			if err := os.WriteFile(path, []byte(testCase.transcript), 0o600); err != nil {
				t.Fatal(err)
			}
			receipt, err := appendResumeCall(
				context.Background(), paths.Values{Home: home}, resumeTarget{ID: id, Path: path},
				"the delivered message", clock.NewFake(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)),
			)
			if err != nil {
				t.Fatalf("appendResumeCall(%s) error = %v", path, err)
			}
			if receipt.SessionID != id || receipt.ParentID != parent {
				t.Fatalf("receipt = %+v, want session %s parent %s", receipt, id, parent)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
			var appended map[string]any
			if err := json.Unmarshal([]byte(lines[len(lines)-1]), &appended); err != nil {
				t.Fatalf("appended line of %s = %q: %v", path, lines[len(lines)-1], err)
			}
			if appended["sessionId"] != id || appended["parentUuid"] != parent {
				t.Fatalf("appended event = %v, want sessionId %s parentUuid %s", appended, id, parent)
			}
		})
	}
}

// The live-session guard reads gather's table: a jail process carrying the
// session id is running, and a jail root never staged holds no process.
func TestRunningClaudeSessionReadsTheProcessTable(t *testing.T) {
	const id = "01234567-89ab-cdef-0123-456789abcdef"
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "4242"), 0o700); err != nil {
		t.Fatal(err)
	}
	environ := resolve.ClaudeSessionEnv + "=" + id + "\x00"
	if err := os.WriteFile(filepath.Join(root, "4242", "environ"), []byte(environ), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		root, id string
		want     bool
	}{
		{root: root, id: id, want: true},
		{root: root, id: "fedcba98-7654-3210-fedc-ba9876543210", want: false},
		{root: filepath.Join(root, "missing-proc"), id: id, want: false},
	} {
		if got, err := runningClaudeSession(tc.root, tc.id); err != nil || got != tc.want {
			t.Errorf("runningClaudeSession(%s, %s) = %t, %v; want %t", tc.root, tc.id, got, err, tc.want)
		}
	}
}
