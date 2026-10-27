package tokens

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The fixture tree (testdata/fixtures/tree): a main session that spawns a
// foreman, which spawns two workers and a tracer. Worker one writes into the
// session's cwd /work/repo and outside it (/tmp, /private/tmp, /work/other);
// worker two writes only under /tmp, so it is silent.
const (
	treeSession = "5e55a0a1-7ee0-4000-8000-00000000c0de"
	treeForeman = "a0f0000000000001"
	treeWorker1 = "a0f0000000000002"
	treeWorker2 = "a0f0000000000003"
	treeTracer  = "a0f0000000000004"
)

// treeAudit runs --tree over root with the fixture price table.
func treeAudit(t *testing.T, root string, args ...string) (int, string, string) {
	t.Helper()
	full := append([]string{"--root", root, "--prices", fixture(t, "model-cost.json")}, args...)
	return auditWith(t, Options{}, full...)
}

// treeJSON runs --tree --json over root and decodes the document.
func treeJSON(t *testing.T, root string, args ...string) (int, treeDocument, string) {
	t.Helper()
	code, stdout, stderr := treeAudit(t, root, append(args, "--json")...)
	var document treeDocument
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("decode --json stdout: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	return code, document, stderr
}

// agentRow is the document's row for id.
func agentRow(t *testing.T, document treeDocument, id string) treeRow {
	t.Helper()
	for i := range document.Agents {
		if document.Agents[i].ID == id {
			return document.Agents[i]
		}
	}
	t.Fatalf("no row for agent %s in %+v", id, document.Agents)
	return treeRow{}
}

// compareGolden compares got with testdata/tree/{name}; PFM_UPDATE_GOLDENS=1
// rewrites it instead.
func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "tree", name)
	if os.Getenv("PFM_UPDATE_GOLDENS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the golden %s: %v", path, err)
	}
	compareText(t, name, string(want), got)
}

// TestTreeSessionReportMatchesItsGolden pins the text report and the JSON
// document of the whole fixture session.
func TestTreeSessionReportMatchesItsGolden(t *testing.T) {
	root := fixture(t, "tree/projects")
	mask := strings.NewReplacer(realPath(t, "testdata/fixtures"), "{FIX}").Replace
	code, stdout, stderr := treeAudit(t, root, "--tree", treeSession)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d stderr %q, want 0 and nothing", code, stderr)
	}
	compareGolden(t, "session.txt", mask(stdout))
	code, stdout, stderr = treeAudit(t, root, "--tree", treeSession, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("--json: exit %d stderr %q, want 0 and nothing", code, stderr)
	}
	compareGolden(t, "session.json", mask(stdout))
}

// TestTreeMeasuresEveryAgentOfTheSession checks the measured numbers against
// the fixture's own: tokens, written bytes inside the scope only, read bytes
// of tool-result text only, the silent share and the per-type ratio.
func TestTreeMeasuresEveryAgentOfTheSession(t *testing.T) {
	code, document, stderr := treeJSON(t, fixture(t, "tree/projects"), "--tree", treeSession)
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	want := []struct {
		id, kind, parent string
		depth            int
		input, written   int64
		read             int64
	}{
		{treeForeman, "flights-foreman", treeSession, 1, 74100, 0, 48},
		{treeSession, "main", "", 0, 48500, 0, 29},
		// Write 12 + Edit 12 + MultiEdit 5+5 + NotebookEdit 5; /tmp, /private/tmp and /work/other count 0
		{treeWorker1, "flights-foreman-sonnet", treeForeman, 2, 22800, 39, 71},
		{treeWorker2, "flights-foreman-sonnet", treeForeman, 2, 10100, 0, 25},
		{treeTracer, "tracer", treeForeman, 2, 6600, 0, 32},
	}
	if len(document.Agents) != len(want) {
		t.Fatalf("%d agent rows, want %d: %+v", len(document.Agents), len(want), document.Agents)
	}
	for i, w := range want {
		row := document.Agents[i]
		parent := ""
		if row.Parent != nil {
			parent = *row.Parent
		}
		if row.ID != w.id || row.Type != w.kind || parent != w.parent || row.Depth == nil || *row.Depth != w.depth ||
			row.InputTokens != w.input || row.WrittenBytes != w.written || row.ReadBytes != w.read || row.USD == nil {
			t.Errorf("row %d = %+v, want %+v", i, row, w)
		}
	}
	tree := document.Tree
	if tree.Root != treeRootSession || tree.WriteScope != "/work/repo" || tree.Agents != 5 || tree.NotRead != 0 ||
		tree.InputTokens != 162100 || tree.WrittenBytes != 39 || tree.ReadBytes != 205 {
		t.Errorf("tree totals %+v", tree)
	}
	if tree.SilentShare == nil || math.Abs(*tree.SilentShare-139300.0/162100.0) > 1e-12 {
		t.Errorf("silent share %v, want 139300/162100", tree.SilentShare)
	}
	if tree.LandedLines != nil || tree.LandedNotMeasured == nil || *tree.LandedNotMeasured != landedNotMeasured {
		t.Errorf("landed %v / %v, want not measured", tree.LandedLines, tree.LandedNotMeasured)
	}
	if document.Excluded != landedRules {
		t.Errorf("excluded %q, want %q", document.Excluded, landedRules)
	}
	workers := document.Types[2]
	if workers.Type != "flights-foreman-sonnet" || workers.Agents != 2 || workers.WrittenReadRatio == nil ||
		math.Abs(*workers.WrittenReadRatio-39.0/96.0) > 1e-12 {
		t.Errorf("types %+v, want flights-foreman-sonnet third with ratio 39/96", document.Types)
	}
}

// TestTreeAgentRootTakesOnlyItsDescendants: an agent id roots the tree at
// that agent; the main session and every agent outside its chain stay out.
func TestTreeAgentRootTakesOnlyItsDescendants(t *testing.T) {
	root := fixture(t, "tree/projects")
	code, document, stderr := treeJSON(t, root, "--tree", treeForeman)
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if document.Tree.Root != treeRootAgent || document.Tree.Session != treeSession || len(document.Agents) != 4 ||
		document.Tree.InputTokens != 113600 {
		t.Fatalf("tree %+v agents %+v, want the foreman and its three children", document.Tree, document.Agents)
	}
	for id, depth := range map[string]int{treeForeman: 0, treeWorker1: 1, treeWorker2: 1, treeTracer: 1} {
		if row := agentRow(t, document, id); row.Depth == nil || *row.Depth != depth {
			t.Errorf("%s depth %v, want %d", id, row.Depth, depth)
		}
	}
	code, document, _ = treeJSON(t, root, "--tree", "agent-"+treeWorker1)
	if code != 0 || len(document.Agents) != 1 || document.Agents[0].ID != treeWorker1 ||
		*document.Agents[0].Depth != 0 {
		t.Fatalf("exit %d agents %+v, want worker one alone at depth 0", code, document.Agents)
	}
}

// TestTreeWorktreeIsTheWriteScope: --worktree replaces the session's cwd as
// the scope written bytes count under.
func TestTreeWorktreeIsTheWriteScope(t *testing.T) {
	if got := writtenBytes("Write", json.RawMessage(`{"file_path":"/work/repo/src/a.go","content":"abc"}`),
		"/work/repo/src"); got != 3 {
		t.Errorf("a write inside the scope = %d, want 3", got)
	}
	for _, path := range []string{"/work/repo/b.go", "/work/repository/a.go", "/tmp/a.go", "/private/tmp/a.go", ""} {
		raw, _ := json.Marshal(map[string]string{"file_path": path, "content": "abc"})
		if got := writtenBytes("Write", raw, "/work/repo/src"); got != 0 {
			t.Errorf("a write to %q = %d, want 0", path, got)
		}
	}
	if got := writtenBytes("Read", json.RawMessage(`{"file_path":"/work/repo/src/a.go","content":"abc"}`),
		"/work/repo/src"); got != 0 {
		t.Errorf("a Read = %d, want 0", got)
	}
	if got := writtenBytes(
		"Edit",
		json.RawMessage(`{"file_path":"src/é.go","new_string":"é"}`),
		"/work/repo",
	); got != 2 {
		t.Errorf("a relative Edit of one two-byte rune = %d, want 2", got)
	}
}

// TestTreeUnreadableAgentIsANamedRow: a sub-agent whose meta cannot be parsed
// and one whose transcript cannot be opened stay rows, named in NOT READ and
// left out of every total, and the run exits 1.
func TestTreeUnreadableAgentIsANamedRow(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	materialize(t, fixture(t, "tree/projects"), root, func(text string) string { return text })
	sub := filepath.Join(root, "-work-repo", treeSession, "subagents")
	broken := filepath.Join(sub, "agent-a0f00000000000b1")
	if err := os.WriteFile(broken+".meta.json", []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken+".jsonl", []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(sub, "agent-a0f00000000000b2")
	if err := os.WriteFile(gone+".meta.json", []byte(`{"agentType":"tracer","parentAgentId":"`+treeForeman+
		`","spawnDepth":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sub, "no-such-transcript.jsonl"), gone+".jsonl"); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := treeAudit(t, root, "--tree", treeSession)
	if code != 1 || !strings.Contains(stdout, "NOT READ                   2 agents — ") ||
		!strings.Contains(stdout, "a0f00000000000b1: meta: parse sub-agent meta") ||
		!strings.Contains(stdout, "a0f00000000000b2: open ") ||
		!strings.Contains(stdout, "  a0f00000000000b2                     tracer                   unreadable: open ") {
		t.Fatalf("exit %d stdout:\n%s\nwant exit 1 naming both unreadable agents", code, stdout)
	}
	code, document, _ := treeJSON(t, root, "--tree", treeSession)
	if code != 1 || document.Tree.NotRead != 2 || document.Tree.Agents != 7 || document.Tree.InputTokens != 162100 {
		t.Fatalf("exit %d tree %+v, want 2 of 7 not read and the totals unchanged", code, document.Tree)
	}
	if row := agentRow(t, document, "a0f00000000000b1"); row.Unreadable == nil || row.Depth != nil ||
		row.USD != nil || row.Parent != nil {
		t.Errorf("broken-meta row %+v, want unreadable with no depth, parent or dollars", row)
	}
}

// TestTreeTargetsThatResolveToNothingAreFailures: an unknown id fails naming
// the roots, never an empty tree; no root at all names the remedy.
func TestTreeTargetsThatResolveToNothingAreFailures(t *testing.T) {
	root := fixture(t, "tree/projects")
	code, stdout, stderr := treeAudit(t, root, "--tree", "no-such-id")
	if code != 1 || stdout != "" ||
		stderr != "pfm tokens: --tree no-such-id: no session or agent transcript of that id under "+root+"\n" {
		t.Errorf("unknown id: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, _, stderr = auditWith(t, Options{Env: &paths.MapEnv{HostnameValue: "test-host"}},
		"--tree", treeSession, "--prices", fixture(t, "model-cost.json"))
	if code != 1 || stderr != "pfm tokens: --tree: no Claude transcript root is configured; pass --root DIR\n" {
		t.Errorf("no root: exit %d stderr %q", code, stderr)
	}
	var document treeDocument
	code, stdout, stderr = auditWith(t, Options{Env: &paths.MapEnv{
		HostnameValue: "test-host", Values: map[string]string{"CLAUDE_CONFIG_DIR": filepath.Dir(root)},
	}}, "--tree", treeSession, "--json", "--prices", fixture(t, "model-cost.json"))
	if err := json.Unmarshal([]byte(stdout), &document); err != nil || code != 0 || document.Tree.Agents != 5 {
		t.Errorf("CLAUDE_CONFIG_DIR root: exit %d, %v, tree %+v (stderr %q), want the session found under its projects",
			code, err, document.Tree, stderr)
	}
}

// TestTreeFlagsGoTogether: --worktree and --base go together, the --tree
// flags need --tree, and --tree refuses another mode or a window.
func TestTreeFlagsGoTogether(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--tree", "s", "--worktree", "/w"}, "--worktree and --base go together"},
		{[]string{"--tree", "s", "--base", "abc"}, "--worktree and --base go together"},
		{[]string{"--tree", "s", "--worktree", "", "--base", "abc"}, "--worktree and --base go together"},
		{[]string{"--json"}, "--json measures a spawn tree: pass --tree with it"},
		{[]string{"--worktree", "/w", "--base", "abc"}, "--worktree measures a spawn tree: pass --tree with it"},
		{[]string{"--tree", "s", "--codex"}, "--tree is its own mode"},
		{[]string{"--tree", "s", "--since", "3d"}, "--tree is its own mode"},
		{[]string{"--tree", "agent-"}, "--tree wants a session id or an agent id"},
		{[]string{"--opencode", "--tree", "s"}, "--opencode is its own mode: it takes no --codex, --tree,"},
	}
	for _, c := range cases {
		code, stdout, stderr := auditWith(t, Options{}, append(c.args, "--prices", fixture(t, "model-cost.json"))...)
		if code != 2 || stdout != "" || !strings.HasPrefix(stderr, "pfm tokens: "+c.want) {
			t.Errorf("%v: exit %d stdout %q stderr %q, want 2 and %q", c.args, code, stdout, stderr, c.want)
		}
	}
}

// TestTreeOutWritesTheDocument: --out writes the same document --json prints
// and the text report names it.
func TestTreeOutWritesTheDocument(t *testing.T) {
	root := fixture(t, "tree/projects")
	out := filepath.Join(t.TempDir(), "tree.json")
	code, stdout, stderr := treeAudit(t, root, "--tree", treeSession, "--out", out)
	if code != 0 || !strings.Contains(stdout, "\nfull data → "+out+" (") {
		t.Fatalf("exit %d stderr %q stdout:\n%s", code, stderr, stdout)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	_, printed, _ := treeAudit(t, root, "--tree", treeSession, "--json")
	if string(written) != printed {
		t.Errorf("--out wrote:\n%s\n--json printed:\n%s", written, printed)
	}
}
