package tokens

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRootsResolveDedupeAndRefuse(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	a, _, _ := testAuditor(t, "--root", root, "--root", link)
	got, err := a.roots()
	if err != nil || !reflect.DeepEqual(got, []string{realPath(t, root)}) {
		t.Errorf("roots = %v, %v; want the one resolved root", got, err)
	}
	missing := filepath.Join(root, "none")
	a, _, _ = testAuditor(t, "--root", missing)
	if _, err := a.roots(); err == nil ||
		!strings.HasPrefix(err.Error(), "--root "+missing+" is not readable: lstat ") {
		t.Errorf("a missing --root = %v, want it named as unreadable", err)
	}
	a, _, _ = testAuditor(t)
	a.claudeRoots = []string{missing, root} // pfm's own roots: a missing one is skipped
	if got, err := a.roots(); err != nil || len(got) != 1 {
		t.Errorf("pfm roots = %v, %v; want the one present", got, err)
	}
	a.claudeRoots = []string{missing}
	if _, err := a.roots(); err == nil || err.Error() != "no transcript root found; pass --root DIR" {
		t.Errorf("no root at all = %v", err)
	}
}

func TestOwnershipDedupesTheSameSessionUnderTwoRoots(t *testing.T) {
	// two accounts' roots: the last three segments name the transcript
	one, two := filepath.Join(t.TempDir(), "projects"), filepath.Join(t.TempDir(), "projects")
	line := assistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", 1, 1, 0, 0)
	first := writeTranscript(t, filepath.Join(one, "-w-demo", "s1.jsonl"), line)
	writeTranscript(t, filepath.Join(two, "-w-demo", "s1.jsonl"), line)
	a, _, _ := testAuditor(t, "--since", "99999d", "--root", one, "--root", two)
	_, files, err := a.ownership("")
	if err != nil || len(files) != 1 || files[0] != realPath(t, first) || a.scan.dupFiles != 1 {
		t.Errorf("ownership = %v, %v, dupFiles %d; want the first copy once", files, err, a.scan.dupFiles)
	}
}

func TestCallOriginsNamedOriginElseFirstUnmarkedElseNone(t *testing.T) {
	dir := t.TempDir()
	call := func(session, extra string) string {
		line := assistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", 1, 1, 0, 0)
		line = strings.Replace(line, `"sessionId":"s1"`, `"sessionId":"`+session+`"`, 1)
		return strings.Replace(line, `{"type":"assistant"`, `{"type":"assistant"`+extra, 1)
	}
	a, _, _ := testAuditor(t)
	named := []string{
		writeTranscript(t, filepath.Join(dir, "named", "a.jsonl"), call("sa", `,"session_id":"sb"`)),
		writeTranscript(t, filepath.Join(dir, "named", "b.jsonl"), call("sb", "")),
	}
	if got := a.callOrigins(named); got["message:m1"] != named[1] {
		t.Errorf("named origin owner = %q, want %s: another copy names it", got["message:m1"], named[1])
	}
	first := []string{
		writeTranscript(t, filepath.Join(dir, "first", "a.jsonl"), call("sa", `,"forkedFrom":{"sessionId":"zz"}`)),
		writeTranscript(t, filepath.Join(dir, "first", "b.jsonl"), call("sb", "")),
		writeTranscript(t, filepath.Join(dir, "first", "c.jsonl"), call("sc", "")),
	}
	if got := a.callOrigins(first); got["message:m1"] != first[1] {
		t.Errorf("unmarked owner = %q, want the first unmarked holder %s", got["message:m1"], first[1])
	}
	forks := []string{
		writeTranscript(t, filepath.Join(dir, "forks", "a.jsonl"), call("sa", `,"forkedFrom":{"sessionId":"zz"}`)),
		writeTranscript(t, filepath.Join(dir, "forks", "b.jsonl"), call("sb", `,"forkedFrom":{"sessionId":"zz"}`)),
	}
	if got, found := a.callOrigins(forks)["message:m1"]; !found || got != "" {
		t.Errorf("all-fork owner = %q (found %v), want \"\": the origin lies outside the scan", got, found)
	}
	if got := a.callOrigins(named[:1]); len(got) != 0 {
		t.Errorf("a call one transcript holds has no owner entry: %v", got)
	}
}

func TestCollectRunsFoldsSubfolderChatsOntoTheirProject(t *testing.T) {
	root := t.TempDir()
	at := "2026-09-21T05:00:00Z"
	writeTranscript(t, filepath.Join(root, "-w-demo", "s1.jsonl"), assistant("m1", "claude-sonnet-5", at, 1, 1, 0, 0))
	sub := strings.Replace(
		assistant("m2", "claude-sonnet-5", at, 1, 1, 0, 0),
		`"cwd":"/w/demo"`,
		`"cwd":"/w/demo/pkg"`,
		1,
	)
	writeTranscript(t, filepath.Join(root, "-w-demo-pkg", "s2.jsonl"), sub)
	a, _, _ := testAuditor(t, "--since", "99999d", "--root", root)
	if err := a.collectRuns(); err != nil {
		t.Fatal(err)
	}
	var projects []string
	for _, r := range a.runs {
		projects = append(projects, r.project)
	}
	if !reflect.DeepEqual(projects, []string{"/w/demo", "/w/demo"}) {
		t.Errorf("projects = %v, want both folded onto /w/demo", projects)
	}
	a, _, _ = testAuditor(t, "--since", "99999d", "--root", root, "--session", "s2")
	if err := a.collectRuns(); err != nil || len(a.runs) != 1 || a.scan.files != 1 {
		t.Fatalf("--session s2 = %v, %d runs over %d files; want one", err, len(a.runs), a.scan.files)
	}
	if want := "--session s2: 1 transcripts outside that session not replayed; ownership read 2 transcripts"; !reflect.DeepEqual(
		a.scan.notes,
		[]string{want},
	) {
		t.Errorf("notes = %q, want %q", a.scan.notes, want)
	}
}
