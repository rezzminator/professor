package corpussan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
	"github.com/rezzminator/professor/pfm/internal/callmeter/backfill"
)

// The fixture is invented: a user "alice" whose project lives at
// /srv/fixture/alice/acme, one chat that runs Bash, Read and Agent, and one
// sub-agent that writes a file outside the project.
const (
	sid     = "11111111-1111-4111-8111-111111111111"
	agentID = "a1b2c3d4e5f6"
)

var mainLines = []string{
	`{"type":"user","timestamp":"2026-09-20T10:00:00.000Z","cwd":"/srv/fixture/alice/acme","sessionId":"` + sid + `","uuid":"u-1","gitBranch":"alice-wip","message":{"role":"user","content":"Please fix Alice's café\nnow"}}`,
	`{"type":"assistant","timestamp":"2026-09-20T10:00:01.000Z","cwd":"/srv/fixture/alice/acme","sessionId":"` + sid + `","uuid":"u-2","parentUuid":"u-1","requestId":"req_1","message":{"id":"msg_A","role":"assistant","model":"claude-test-1","usage":{"input_tokens":10,"cache_read_input_tokens":200,"cache_creation_input_tokens":30,"output_tokens":7},"content":[` +
		`{"type":"thinking","thinking":"secret plan for alice","signature":"c2lnbmF0dXJl"},` +
		`{"type":"text","text":"On it, Alice."},` +
		`{"type":"tool_use","id":"toolu_B","name":"Bash","input":{"command":"cd /srv/fixture/alice/acme/pfm && grep -n alice /private/tmp/notes.txt | head -5 > /dev/null","description":"search for Alice"}},` +
		`{"type":"tool_use","id":"toolu_R","name":"Read","input":{"file_path":"/srv/fixture/alice/acme/pfm/store.go","offset":3,"limit":2}},` +
		`{"type":"tool_use","id":"toolu_T","name":"Agent","input":{"subagent_type":"general-purpose","description":"look","prompt":"read Alice's file"}}]}}`,
	`{"type":"user","timestamp":"2026-09-20T10:00:02.000Z","cwd":"/srv/fixture/alice/acme","sessionId":"` + sid + `","uuid":"u-3","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_B","content":"alice line\n"}]},"toolUseResult":{"stdout":"alice line\n","stderr":"","interrupted":false}}`,
	`{"type":"user","timestamp":"2026-09-20T10:00:03.000Z","cwd":"/srv/fixture/alice/acme","sessionId":"` + sid + `","uuid":"u-4","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_R","content":[{"type":"text","text":"   3\tpackage x\n   4\t// alice\n"}]}]},"toolUseResult":{"type":"text","file":{"filePath":"/srv/fixture/alice/acme/pfm/store.go","content":"package x\n// alice\n","numLines":2,"startLine":3,"totalLines":9}}}`,
	`{"type":"user","timestamp":"2026-09-20T10:00:09.000Z","cwd":"/srv/fixture/alice/acme","sessionId":"` + sid + `","uuid":"u-5","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_T","content":[{"type":"text","text":"done by alice"}]}]},"toolUseResult":{"status":"completed","agentId":"` + agentID + `","agentType":"general-purpose","totalTokens":1234,"totalToolUseCount":1,"resolvedModel":"claude-test-1","prompt":"read Alice's file","content":[{"type":"text","text":"done by alice"}]}}`,
}

var subLines = []string{
	`{"type":"assistant","timestamp":"2026-09-20T10:00:05.000Z","cwd":"/srv/fixture/alice/acme","sessionId":"` + sid + `","agentId":"` + agentID + `","uuid":"s-1","message":{"id":"msg_S","role":"assistant","model":"claude-test-1","usage":{"input_tokens":5,"cache_read_input_tokens":50,"cache_creation_input_tokens":5,"output_tokens":3},"content":[{"type":"tool_use","id":"toolu_W","name":"Write","input":{"file_path":"/srv/fixture/alice/other/x.md","content":"héllo alice"}}]}}`,
	`{"type":"user","timestamp":"2026-09-20T10:00:06.000Z","cwd":"/srv/fixture/alice/acme","sessionId":"` + sid + `","agentId":"` + agentID + `","uuid":"s-2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_W","content":"File created at /srv/fixture/alice/other/x.md"}]},"toolUseResult":{"type":"create","filePath":"/srv/fixture/alice/other/x.md","content":"héllo alice","structuredPatch":[],"originalFile":null}}`,
}

const subMeta = `{"agentType":"general-purpose","description":"look at alice","toolUseId":"toolu_T"}`

var testOpts = Options{
	Project: "/srv/fixture/alice/acme",
	Home:    "/srv/fixture/alice",
	Allow:   []string{"pfm", "store", "acme"},
	Deny:    []string{"alice"},
}

// writeFixture lays the fixture out as a Claude config dir and returns the
// config dir and the session transcript's path.
func writeFixture(t *testing.T) (string, string) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), ".claude")
	proj := filepath.Join(cfg, "projects", "-srv-fixture-alice-acme")
	sub := filepath.Join(proj, sid, "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	session := filepath.Join(proj, sid+".jsonl")
	for path, body := range map[string]string{
		session: strings.Join(mainLines, "\n") + "\n",
		filepath.Join(sub, "agent-"+agentID+".jsonl"):     strings.Join(subLines, "\n") + "\n",
		filepath.Join(sub, "agent-"+agentID+".meta.json"): subMeta,
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	return cfg, session
}

// sanitize runs the sanitizer over the fixture into a fresh config dir and
// returns that config dir.
func sanitize(t *testing.T, session string) string {
	t.Helper()
	s, err := NewSanitizer(testOpts)
	if err != nil {
		t.Fatalf("NewSanitizer: %v", err)
	}
	cfg := filepath.Join(t.TempDir(), ".claude")
	if _, err := s.Session(session, filepath.Join(cfg, "projects", "-tmp-demo-proj")); err != nil {
		t.Fatalf("Session: %v", err)
	}
	return cfg
}

// outputs reads every file under root, keyed by its path relative to root.
func outputs(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		files[rel] = raw
		return err
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return files
}

// lines decodes every line of a sanitized transcript.
func lines(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	for i, line := range bytes.Split(bytes.TrimSuffix(raw, []byte("\n")), []byte("\n")) {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("line %d is not JSON: %v: %s", i+1, err, line)
		}
		out = append(out, m)
	}
	return out
}

// at walks a decoded value by object keys and array indexes.
func at(t *testing.T, v any, path ...any) any {
	t.Helper()
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("at %v: %T is not an object", path, v)
			}
			v = m[k]
		case int:
			a, ok := v.([]any)
			if !ok || k >= len(a) {
				t.Fatalf("at %v: %T has no index %d", path, v, k)
			}
			v = a[k]
		}
	}
	return v
}

func sanitizedFiles(t *testing.T) (map[string][]byte, string) {
	t.Helper()
	_, session := writeFixture(t)
	cfg := sanitize(t, session)
	proj := filepath.Join(cfg, "projects", "-tmp-demo-proj")
	return outputs(t, proj), proj
}

func TestSessionWritesEveryFile(t *testing.T) {
	files, _ := sanitizedFiles(t)
	var names []string
	for name := range files {
		names = append(names, name)
	}
	want := []string{
		sid + ".jsonl",
		filepath.Join(sid, "subagents", "agent-"+agentID+".jsonl"),
		filepath.Join(sid, "subagents", "agent-"+agentID+".meta.json"),
	}
	if len(names) != len(want) {
		t.Fatalf("files = %v, want %v", names, want)
	}
	for _, w := range want {
		if _, ok := files[w]; !ok {
			t.Errorf("missing output %s (have %v)", w, names)
		}
	}
}

func TestSessionFillsProseAtSameLength(t *testing.T) {
	files, _ := sanitizedFiles(t)
	main := lines(t, files[sid+".jsonl"])
	sub := lines(t, files[filepath.Join(sid, "subagents", "agent-"+agentID+".jsonl")])
	cases := []struct {
		name string
		got  any
		orig string
	}{
		{"user prompt", at(t, main[0], "message", "content"), "Please fix Alice's café\nnow"},
		{"thinking", at(t, main[1], "message", "content", 0, "thinking"), "secret plan for alice"},
		{"assistant text", at(t, main[1], "message", "content", 1, "text"), "On it, Alice."},
		{"bash description", at(t, main[1], "message", "content", 2, "input", "description"), "search for Alice"},
		{"agent prompt", at(t, main[1], "message", "content", 4, "input", "prompt"), "read Alice's file"},
		{"bash result", at(t, main[2], "message", "content", 0, "content"), "alice line\n"},
		{"stdout", at(t, main[2], "toolUseResult", "stdout"), "alice line\n"},
		{
			"read result text",
			at(t, main[3], "message", "content", 0, "content", 0, "text"),
			"   3\tpackage x\n   4\t// alice\n",
		},
		{"read file content", at(t, main[3], "toolUseResult", "file", "content"), "package x\n// alice\n"},
		{"agent result", at(t, main[4], "toolUseResult", "content", 0, "text"), "done by alice"},
		{"write content", at(t, sub[0], "message", "content", 0, "input", "content"), "héllo alice"},
		{"write result content", at(t, sub[1], "toolUseResult", "content"), "héllo alice"},
		{"git branch", main[0]["gitBranch"], "alice-wip"},
	}
	for _, c := range cases {
		got, ok := c.got.(string)
		if !ok {
			t.Errorf("%s: %T, want a string", c.name, c.got)
			continue
		}
		if len(got) != len(c.orig) {
			t.Errorf("%s: %d bytes %q, want %d", c.name, len(got), got, len(c.orig))
		}
		if strings.Trim(got, "x\n") != "" {
			t.Errorf("%s: %q is not filler", c.name, got)
		}
		if strings.Count(got, "\n") != strings.Count(c.orig, "\n") {
			t.Errorf("%s: %q lost its line breaks", c.name, got)
		}
	}
}

func TestSessionRewritesPaths(t *testing.T) {
	files, _ := sanitizedFiles(t)
	main := lines(t, files[sid+".jsonl"])
	sub := lines(t, files[filepath.Join(sid, "subagents", "agent-"+agentID+".jsonl")])
	cases := []struct {
		name string
		got  any
		want string
	}{
		{"cwd", main[0]["cwd"], "/tmp/demo-proj"},
		{
			"bash command", at(t, main[1], "message", "content", 2, "input", "command"),
			"cd /tmp/demo-proj/pfm && grep -n xxxxx /tmp/demo-home/private/tmp/xxxxx.txt | head -5 > /dev/null",
		},
		{"read path", at(t, main[1], "message", "content", 3, "input", "file_path"), "/tmp/demo-proj/pfm/store.go"},
		{"read result path", at(t, main[3], "toolUseResult", "file", "filePath"), "/tmp/demo-proj/pfm/store.go"},
		{"write path", at(t, sub[0], "message", "content", 0, "input", "file_path"), "/tmp/demo-home/xxxxx/x.md"},
		{"write result path", at(t, sub[1], "toolUseResult", "filePath"), "/tmp/demo-home/xxxxx/x.md"},
		{"sub cwd", sub[0]["cwd"], "/tmp/demo-proj"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %s", c.name, c.got, c.want)
		}
	}
}

func TestSessionKeepsIDsAndNumbers(t *testing.T) {
	files, _ := sanitizedFiles(t)
	main := lines(t, files[sid+".jsonl"])
	sub := lines(t, files[filepath.Join(sid, "subagents", "agent-"+agentID+".jsonl")])
	var meta map[string]any
	if err := json.Unmarshal(files[filepath.Join(sid, "subagents", "agent-"+agentID+".meta.json")], &meta); err != nil {
		t.Fatalf("meta: %v", err)
	}
	cases := []struct {
		name string
		got  any
		want any
	}{
		{"session id", main[0]["sessionId"], sid},
		{"uuid", main[1]["uuid"], "u-2"},
		{"parent uuid", main[1]["parentUuid"], "u-1"},
		{"request id", main[1]["requestId"], "req_1"},
		{"message id", at(t, main[1], "message", "id"), "msg_A"},
		{"model", at(t, main[1], "message", "model"), "claude-test-1"},
		{"input tokens", at(t, main[1], "message", "usage", "input_tokens"), 10.0},
		{"cache read", at(t, main[1], "message", "usage", "cache_read_input_tokens"), 200.0},
		{"cache write", at(t, main[1], "message", "usage", "cache_creation_input_tokens"), 30.0},
		{"output tokens", at(t, main[1], "message", "usage", "output_tokens"), 7.0},
		{"tool name", at(t, main[1], "message", "content", 2, "name"), "Bash"},
		{"tool use id", at(t, main[1], "message", "content", 2, "id"), "toolu_B"},
		{"read offset", at(t, main[1], "message", "content", 3, "input", "offset"), 3.0},
		{"subagent type", at(t, main[1], "message", "content", 4, "input", "subagent_type"), "general-purpose"},
		{"result tool use id", at(t, main[2], "message", "content", 0, "tool_use_id"), "toolu_B"},
		{"interrupted", at(t, main[2], "toolUseResult", "interrupted"), false},
		{"num lines", at(t, main[3], "toolUseResult", "file", "numLines"), 2.0},
		{"start line", at(t, main[3], "toolUseResult", "file", "startLine"), 3.0},
		{"total lines", at(t, main[3], "toolUseResult", "file", "totalLines"), 9.0},
		{"result agent id", at(t, main[4], "toolUseResult", "agentId"), agentID},
		{"status", at(t, main[4], "toolUseResult", "status"), "completed"},
		{"total tokens", at(t, main[4], "toolUseResult", "totalTokens"), 1234.0},
		{"tool use count", at(t, main[4], "toolUseResult", "totalToolUseCount"), 1.0},
		{"resolved model", at(t, main[4], "toolUseResult", "resolvedModel"), "claude-test-1"},
		{"sub agent id", sub[0]["agentId"], agentID},
		{"sub session id", sub[1]["sessionId"], sid},
		{"write type", at(t, sub[1], "toolUseResult", "type"), "create"},
		{"original file", at(t, sub[1], "toolUseResult", "originalFile"), nil},
		{"meta agent type", meta["agentType"], "general-purpose"},
		{"meta tool use id", meta["toolUseId"], "toolu_T"},
		{"timestamp", sub[0]["timestamp"], "2026-09-20T10:00:05.000Z"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestSessionLeaksNothing(t *testing.T) {
	files, _ := sanitizedFiles(t)
	for name, raw := range files {
		low := strings.ToLower(string(raw))
		for _, word := range []string{"alice", "fixture", "café", "secret", "please", "notes"} {
			if strings.Contains(low, word) {
				t.Errorf("%s still holds %q:\n%s", name, word, raw)
			}
		}
	}
}

func TestSessionIsDeterministic(t *testing.T) {
	_, session := writeFixture(t)
	first := outputs(t, filepath.Join(sanitize(t, session), "projects"))
	second := outputs(t, filepath.Join(sanitize(t, session), "projects"))
	if len(first) != len(second) {
		t.Fatalf("runs wrote %d and %d files", len(first), len(second))
	}
	for name, raw := range first {
		if !bytes.Equal(raw, second[name]) {
			t.Errorf("%s differs between two runs", name)
		}
	}
}

func TestSessionRefusesOverwrite(t *testing.T) {
	_, session := writeFixture(t)
	s, err := NewSanitizer(testOpts)
	if err != nil {
		t.Fatalf("NewSanitizer: %v", err)
	}
	out := t.TempDir()
	if _, err := s.Session(session, out); err != nil {
		t.Fatalf("first Session: %v", err)
	}
	if _, err := s.Session(session, out); err == nil {
		t.Fatal("second Session into the same dir overwrote its output")
	}
}

func TestSessionNamesAMalformedLine(t *testing.T) {
	_, session := writeFixture(t)
	bad := mainLines[0] + "\n" + `{"type":"user", not json` + "\n"
	if err := os.WriteFile(session, []byte(bad), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := NewSanitizer(testOpts)
	if err != nil {
		t.Fatalf("NewSanitizer: %v", err)
	}
	_, err = s.Session(session, t.TempDir())
	if err == nil {
		t.Fatal("a line that is not JSON was accepted")
	}
	if !strings.Contains(err.Error(), session) || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %q does not name the file and line 2", err)
	}
}

func TestNewRejectsRelativeRoots(t *testing.T) {
	for _, o := range []Options{
		{Project: "acme", Home: "/srv/fixture/alice"},
		{Project: "/srv/fixture/alice/acme", Home: ""},
	} {
		if _, err := NewSanitizer(o); err == nil {
			t.Errorf("NewSanitizer(%+v) accepted a root that is not absolute", o)
		}
	}
}

// backfillRows runs backfill over one config dir and renders the columns that
// must not depend on a path: counts, bytes, lines, tokens, ids.
func backfillRows(t *testing.T, cfg string) string {
	t.Helper()
	store, err := callmeter.OpenDB(context.Background(), filepath.Join(t.TempDir(), "callmeter.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	}()
	summary, err := backfill.FromTranscripts(context.Background(), store, []string{cfg}, time.Time{}, t.Logf)
	if err != nil {
		t.Fatalf("backfill %s: %v", cfg, err)
	}
	if len(summary.Unreadable) > 0 || len(summary.Malformed) > 0 {
		t.Fatalf("backfill %s: %s", cfg, summary)
	}
	var out strings.Builder
	for _, query := range []string{
		`SELECT tool_use_id, session_id, agent_id, agent_type, request_id, ts, tool, failed,
			bytes_real, bytes_delivered, read_start, read_lines, read_total_lines, file_bytes, file_bytes_before
			FROM calls ORDER BY tool_use_id`,
		`SELECT request_id, session_id, agent_id, ts, context_tokens, output_tokens, calls FROM requests ORDER BY request_id`,
		`SELECT agent_id, session_id, agent_type, parent_tool_use_id, started, stopped, total_tokens, tool_uses, model
			FROM agents ORDER BY agent_id`,
	} {
		rows, err := store.DB().QueryContext(context.Background(), query)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		names, err := rows.Columns()
		if err != nil {
			t.Fatalf("columns: %v", err)
		}
		for rows.Next() {
			values := make([]any, len(names))
			targets := make([]any, len(names))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatalf("scan: %v", err)
			}
			fmt.Fprintln(&out, values...)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close rows: %v", err)
		}
	}
	return out.String()
}

func TestSessionStillBackfills(t *testing.T) {
	orig, session := writeFixture(t)
	want := backfillRows(t, orig)
	got := backfillRows(t, sanitize(t, session))
	if strings.Count(want, "\n") != 7 { // 4 calls, 2 requests, 1 agent
		t.Fatalf("the original fixture backfilled to:\n%s", want)
	}
	if got != want {
		t.Errorf("sanitized backfill differs\n got:\n%s\nwant:\n%s", got, want)
	}
}
