package backfill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

// since is the window start every test passes: the fixture's current entries
// fall on 2026-09-20, its one old exchange on 2026-08-01.
var since = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

// fixtureHome copies testdata/demo-home into a temp dir and returns the Claude
// config dir inside it.
func fixtureHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "demo-home")
	if err := os.CopyFS(home, os.DirFS(filepath.Join("testdata", "demo-home"))); err != nil {
		t.Fatalf("copy fixture tree: %v", err)
	}
	return filepath.Join(home, ".claude")
}

func openStore(t *testing.T) *callmeter.Store {
	t.Helper()
	store, err := callmeter.OpenDB(context.Background(), filepath.Join(t.TempDir(), "callmeter.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

func runBackfill(t *testing.T, store *callmeter.Store, dirs []string, from time.Time) Summary {
	t.Helper()
	summary, err := FromTranscripts(context.Background(), store, dirs, from, t.Logf)
	if err != nil {
		t.Fatalf("FromTranscripts: %v", err)
	}
	return summary
}

// dump renders every calls, requests and agents row, one line each, so two
// runs can be compared column for column.
func dump(t *testing.T, store *callmeter.Store) string {
	t.Helper()
	var out strings.Builder
	for _, query := range []string{
		"SELECT * FROM calls ORDER BY tool_use_id",
		"SELECT * FROM requests ORDER BY request_id",
		"SELECT * FROM agents ORDER BY agent_id",
	} {
		rows, err := store.DB().QueryContext(context.Background(), query)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		names, err := rows.Columns()
		if err != nil {
			t.Fatalf("%s columns: %v", query, err)
		}
		for rows.Next() {
			values := make([]any, len(names))
			targets := make([]any, len(names))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatalf("%s scan: %v", query, err)
			}
			fmt.Fprintln(&out, values...)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("%s rows: %v", query, err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("%s close: %v", query, err)
		}
	}
	return out.String()
}

func count(t *testing.T, store *callmeter.Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := store.DB().QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

type callRow struct {
	session, agent, agentType, request, tool, input, cwd, source, configDir, errText, persisted *string
	ts, bytesReal, bytesDelivered, fileBytes                                                    *int64
	failed                                                                                      *bool
}

func call(t *testing.T, store *callmeter.Store, id string) callRow {
	t.Helper()
	var c callRow
	err := store.DB().QueryRowContext(context.Background(), `SELECT session_id, agent_id, agent_type, request_id,
		tool, input, cwd, source, config_dir, error, persisted_path, ts, bytes_real, bytes_delivered, file_bytes, failed
		FROM calls WHERE tool_use_id = ?`, id).Scan(&c.session, &c.agent, &c.agentType, &c.request, &c.tool, &c.input,
		&c.cwd, &c.source, &c.configDir, &c.errText, &c.persisted, &c.ts, &c.bytesReal, &c.bytesDelivered, &c.fileBytes,
		&c.failed)
	if err != nil {
		t.Fatalf("read call %s: %v", id, err)
	}
	return c
}

func str(p *string) string {
	if p == nil {
		return "<NULL>"
	}
	return *p
}

func num(p *int64) string {
	if p == nil {
		return "<NULL>"
	}
	return fmt.Sprint(*p)
}

func TestRunImportsFixtureTree(t *testing.T) {
	store := openStore(t)
	dir := fixtureHome(t)
	summary := runBackfill(t, store, []string{dir}, since)
	configDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve config dir: %v", err)
	}

	if summary.TranscriptsRead != 2 || summary.CallsInserted != 8 || summary.CallsFilled != 0 ||
		summary.Requests != 5 || summary.Agents != 1 || len(summary.Unreadable)+len(summary.Malformed) != 0 {
		t.Fatalf("summary = %#v, want 2 transcripts, 8 calls inserted, 5 requests, 1 agent, no problems", summary)
	}
	if n := count(t, store, "SELECT COUNT(*) FROM calls"); n != 8 {
		t.Fatalf("calls = %d, want 8", n)
	}

	a := call(t, store, "toolu_A")
	if str(a.tool) != "Read" || str(a.session) != "sess-demo" || a.agent != nil || str(a.request) != "msg_1" ||
		str(a.cwd) != "/tmp/demo-proj" || num(a.bytesDelivered) != "8" || a.failed == nil || *a.failed ||
		str(a.source) != callmeter.SourceTranscript || str(a.configDir) != configDir || a.fileBytes != nil ||
		num(a.ts) != fmt.Sprint(time.Date(2026, 9, 20, 10, 0, 5, 0, time.UTC).UnixMilli()) ||
		str(a.input) != `{"file_path":"/tmp/demo-proj/notes.md"}` {
		t.Errorf("toolu_A = %s %s %v %s %s %s delivered=%s failed=%v %s %s file=%s ts=%s %s", str(a.tool),
			str(a.session), a.agent, str(a.request), str(a.cwd), str(a.source), num(a.bytesDelivered), a.failed,
			str(a.configDir), configDir, num(a.fileBytes), num(a.ts), str(a.input))
	}
	b := call(t, store, "toolu_B")
	if num(b.bytesDelivered) != fmt.Sprint(len("1 /tmp/demo-proj/notes.md")) ||
		num(b.bytesReal) != fmt.Sprint(len("1 /tmp/demo-proj/notes.md\n")) {
		t.Errorf("toolu_B delivered=%s real=%s", num(b.bytesDelivered), num(b.bytesReal))
	}
	c := call(t, store, "toolu_C")
	if num(c.bytesDelivered) != fmt.Sprint(len("notes")+len("\nagentId: a1demo")) ||
		strings.Contains(str(c.input), "Read notes.md") {
		t.Errorf("toolu_C delivered=%s input=%s (prompt must be dropped)", num(c.bytesDelivered), str(c.input))
	}
	d := call(t, store, "toolu_D")
	if d.failed == nil || !*d.failed || str(d.errText) != "Exit code 1" {
		t.Errorf("toolu_D failed=%v error=%s", d.failed, str(d.errText))
	}
	s1 := call(t, store, "toolu_S1")
	if str(s1.agent) != "a1demo" || str(s1.agentType) != "general-purpose" || str(s1.request) != "msg_s1" ||
		str(s1.session) != "sess-demo" {
		t.Errorf("toolu_S1 agent=%s type=%s request=%s session=%s", str(s1.agent), str(s1.agentType),
			str(s1.request), str(s1.session))
	}

	var contextTokens, outputTokens, calls, pending int64
	if err := store.DB().QueryRowContext(context.Background(),
		"SELECT context_tokens, output_tokens, calls, pending FROM requests WHERE request_id = 'msg_1'").
		Scan(&contextTokens, &outputTokens, &calls, &pending); err != nil {
		t.Fatalf("read request msg_1: %v", err)
	}
	if contextTokens != 1110 || outputTokens != 50 || calls != 2 || pending != 0 {
		t.Errorf("msg_1 context=%d output=%d calls=%d pending=%d, want 1110 50 2 0",
			contextTokens, outputTokens, calls, pending)
	}
	if n := count(
		t,
		store,
		"SELECT COUNT(*) FROM requests WHERE request_id = 'msg_s1' AND agent_id = 'a1demo' AND context_tokens = 904",
	); n != 1 {
		t.Errorf("sub-agent request msg_s1 rows = %d, want 1 with agent a1demo and 904 context tokens", n)
	}

	var parent, agentType, model string
	var total, uses int64
	if err := store.DB().QueryRowContext(context.Background(),
		"SELECT parent_tool_use_id, agent_type, model, total_tokens, tool_uses FROM agents WHERE agent_id = 'a1demo'").
		Scan(&parent, &agentType, &model, &total, &uses); err != nil {
		t.Fatalf("read agent a1demo: %v", err)
	}
	if parent != "toolu_C" || agentType != "general-purpose" || model != "claude-haiku-4-5" || total != 18107 ||
		uses != 1 {
		t.Errorf("agent a1demo = %s %s %s %d %d", parent, agentType, model, total, uses)
	}
	if n := count(t, store, "SELECT COUNT(*) FROM faults"); n != 0 {
		t.Errorf("faults = %d, want 0 (the trailing partial line is a writer mid-append, not a fault)", n)
	}
}

// fileRow is the file columns of one calls row, each rendered by num/str.
type fileRow struct {
	path                                        *string
	start, lines, total, bytesBefore, fileBytes *int64
}

func fileColumnsOf(t *testing.T, store *callmeter.Store, id string) fileRow {
	t.Helper()
	var f fileRow
	err := store.DB().QueryRowContext(context.Background(), `SELECT file_path, read_start, read_lines,
		read_total_lines, file_bytes_before, file_bytes FROM calls WHERE tool_use_id = ?`, id).
		Scan(&f.path, &f.start, &f.lines, &f.total, &f.bytesBefore, &f.fileBytes)
	if err != nil {
		t.Fatalf("read file columns of %s: %v", id, err)
	}
	return f
}

func TestRunFillsFileColumns(t *testing.T) {
	store := openStore(t)
	runBackfill(t, store, []string{fixtureHome(t)}, since)
	cases := []struct {
		id, path, start, lines, total, before string
	}{
		// input only, no offset: the read starts at line 1, its length unknown
		{"toolu_A", "/tmp/demo-proj/notes.md", "1", "<NULL>", "<NULL>", "<NULL>"},
		// input only, ranged
		{"toolu_S1", "/tmp/demo-proj/notes.md", "1", "1", "<NULL>", "<NULL>"},
		// relative path made absolute against cwd; the result's range wins over the input's limit
		{"toolu_R", "/tmp/demo-proj/docs/guide.md", "10", "4", "13", "<NULL>"},
		// a created file had no bytes before
		{"toolu_W", "/tmp/demo-proj/out.md", "<NULL>", "<NULL>", "<NULL>", "0"},
		{"toolu_E", "/tmp/demo-proj/notes.md", "<NULL>", "<NULL>", "<NULL>", fmt.Sprint(len("notes\n"))},
		// not a file tool
		{"toolu_B", "<NULL>", "<NULL>", "<NULL>", "<NULL>", "<NULL>"},
	}
	for _, tc := range cases {
		f := fileColumnsOf(t, store, tc.id)
		got := []string{str(f.path), num(f.start), num(f.lines), num(f.total), num(f.bytesBefore)}
		want := []string{tc.path, tc.start, tc.lines, tc.total, tc.before}
		if strings.Join(got, " ") != strings.Join(want, " ") || f.fileBytes != nil {
			t.Errorf("%s path/start/lines/total/before = %v file_bytes=%s, want %v and <NULL>", tc.id, got,
				num(f.fileBytes), want)
		}
	}
	if n := count(t, store, "SELECT COUNT(*) FROM calls WHERE tool = 'Read' AND file_path IS NOT NULL"); n != 3 {
		t.Errorf("Read rows with a file_path = %d, want 3 (report files counts only those)", n)
	}
}

func TestRunTwiceChangesNothing(t *testing.T) {
	store := openStore(t)
	dir := fixtureHome(t)
	runBackfill(t, store, []string{dir}, since)
	before := dump(t, store)
	second := runBackfill(t, store, []string{dir}, since)
	if second.CallsInserted != 0 || second.CallsFilled != 0 {
		t.Errorf("second run inserted %d and filled %d calls, want 0 and 0", second.CallsInserted, second.CallsFilled)
	}
	if after := dump(t, store); after != before || before == "" {
		t.Errorf("second run changed the store:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestRunKeepsHookValues(t *testing.T) {
	store := openStore(t)
	dir := fixtureHome(t)
	if err := store.UpsertCall(context.Background(), callmeter.Call{
		ToolUseID:      "toolu_A",
		SessionID:      callmeter.Ptr("sess-demo"),
		Tool:           callmeter.Ptr("Read"),
		BytesDelivered: callmeter.Ptr(int64(999)),
		FileBytes:      callmeter.Ptr(int64(42)),
		Source:         callmeter.Ptr(callmeter.SourceHook),
	}, callmeter.Overwrite); err != nil {
		t.Fatalf("seed hook row: %v", err)
	}
	summary := runBackfill(t, store, []string{dir}, since)
	if summary.CallsInserted != 7 || summary.CallsFilled != 1 {
		t.Errorf("summary inserted %d filled %d, want 7 and 1", summary.CallsInserted, summary.CallsFilled)
	}
	a := call(t, store, "toolu_A")
	if num(a.bytesDelivered) != "999" || num(a.fileBytes) != "42" || str(a.source) != callmeter.SourceHook {
		t.Errorf("hook values overwritten: delivered=%s file=%s source=%s", num(a.bytesDelivered), num(a.fileBytes),
			str(a.source))
	}
	if str(a.request) != "msg_1" || str(a.cwd) != "/tmp/demo-proj" || a.ts == nil {
		t.Errorf("NULL columns not filled: request=%s cwd=%s ts=%s", str(a.request), str(a.cwd), num(a.ts))
	}
}

func TestRunSkipsEntriesOlderThanSince(t *testing.T) {
	store := openStore(t)
	dir := fixtureHome(t)
	summary := runBackfill(t, store, []string{dir}, since)
	if n := count(t, store, "SELECT COUNT(*) FROM calls WHERE tool_use_id = 'toolu_old'"); n != 0 {
		t.Errorf("toolu_old imported although older than since")
	}
	if n := count(t, store, "SELECT COUNT(*) FROM requests WHERE request_id = 'msg_old'"); n != 0 {
		t.Errorf("msg_old imported although older than since")
	}
	if summary.EntriesSkipped == 0 {
		t.Errorf("summary counts no skipped entry: %#v", summary)
	}
	runBackfill(t, store, []string{dir}, time.Time{})
	if n := count(t, store, "SELECT COUNT(*) FROM calls WHERE tool_use_id = 'toolu_old'"); n != 1 {
		t.Errorf("with no window toolu_old rows = %d, want 1", n)
	}
}

func TestRunReportsUnreadableTranscriptAndContinues(t *testing.T) {
	store := openStore(t)
	dir := fixtureHome(t)
	project := filepath.Join(dir, "projects", "-tmp-demo-proj")
	broken := filepath.Join(project, "broken.jsonl")
	if err := os.Mkdir(broken, 0o755); err != nil {
		t.Fatalf("make directory transcript: %v", err)
	}
	bad := filepath.Join(project, "bad.jsonl")
	good := `{"type":"assistant","timestamp":"2026-09-20T11:00:00.000Z","cwd":"/tmp/demo-proj","sessionId":"bad",` +
		`"message":{"id":"msg_bad","content":[{"type":"tool_use","id":"toolu_bad","name":"Read",` +
		`"input":{"file_path":"/tmp/demo-proj/notes.md"}}],"usage":{"input_tokens":1,"output_tokens":1}}}`
	if err := os.WriteFile(bad, []byte("not json\n"+good+"\n"), 0o600); err != nil {
		t.Fatalf("write malformed transcript: %v", err)
	}
	summary := runBackfill(t, store, []string{dir}, since)

	if len(summary.Unreadable) != 1 || summary.Unreadable[0].Path != broken {
		t.Errorf("unreadable = %+v, want exactly %s", summary.Unreadable, broken)
	}
	if len(summary.Malformed) != 1 || summary.Malformed[0].Path != bad || summary.Malformed[0].Line != 1 {
		t.Errorf("malformed = %+v, want %s line 1", summary.Malformed, bad)
	}
	if summary.CallsInserted != 9 || summary.TranscriptsRead != 3 {
		t.Errorf("summary = %#v, want the other 3 transcripts read and 9 calls inserted", summary)
	}
	if n := count(t, store, "SELECT COUNT(*) FROM faults WHERE stage = ? AND error LIKE ?",
		callmeter.StageBackfill, "%"+broken+"%"); n != 1 {
		t.Errorf("faults naming %s = %d, want 1", broken, n)
	}
	if n := count(t, store, "SELECT COUNT(*) FROM faults WHERE stage = ? AND error LIKE ?",
		callmeter.StageBackfill, "%"+bad+" line 1%"); n != 1 {
		t.Errorf("faults naming %s line 1 = %d, want 1", bad, n)
	}
	text := summary.String()
	if !strings.Contains(text, "unreadable: "+broken) || !strings.Contains(text, "malformed: "+bad+" line 1") ||
		!strings.Contains(text, "1 unreadable") {
		t.Errorf("summary text does not name the problems:\n%s", text)
	}
}

func TestRunNamesConfigDirWithoutProjects(t *testing.T) {
	store := openStore(t)
	empty := t.TempDir()
	summary := runBackfill(t, store, []string{empty}, since)
	if len(summary.NoProjects) != 1 || summary.NoProjects[0] != empty {
		t.Errorf("no-projects = %v, want [%s]", summary.NoProjects, empty)
	}
	if text := summary.String(); !strings.Contains(text, "no projects/ in config dir "+empty) {
		t.Errorf("summary text does not name %s:\n%s", empty, text)
	}
}

// TestRunRecordsMalformedInputAsFailedCall pins § "Malformed calls are calls":
// a tool_use whose input the harness rejected is a failed call, never a
// malformed line; a line that is not JSON stays malformed.
func TestRunRecordsMalformedInputAsFailedCall(t *testing.T) {
	store := openStore(t)
	dir := filepath.Join(t.TempDir(), ".claude")
	project := filepath.Join(dir, "projects", "-tmp-demo-proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("make project dir: %v", err)
	}
	path := filepath.Join(project, "sess-bad-input.jsonl")
	head := `{"type":"assistant","timestamp":"2026-09-20T11:00:00.000Z","cwd":"/tmp/demo-proj",` +
		`"sessionId":"sess-bad-input","message":{"id":"msg_bad_input","content":[`
	lines := []string{
		head + `{"type":"tool_use","id":"toolu_nopath","name":"Read","input":{"limit":10}},` +
			`{"type":"tool_use","id":"toolu_arrayoffset","name":"Read",` +
			`"input":{"file_path":"/tmp/demo-proj/notes.md","offset":[1,2]}}],` +
			`"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"user","timestamp":"2026-09-20T11:00:01.000Z","cwd":"/tmp/demo-proj","sessionId":"sess-bad-input",` +
			`"message":{"content":[{"type":"tool_result","tool_use_id":"toolu_nopath","is_error":true,` +
			`"content":"<tool_use_error>InputValidationError: file_path is missing</tool_use_error>"}]},` +
			`"toolUseResult":"InputValidationError: file_path is missing"}`,
		`{"type":"user","timestamp":"2026-09-20T11:00:02.000Z","cwd":"/tmp/demo-proj","sessionId":"sess-bad-input",` +
			`"message":{"content":[{"type":"tool_result","tool_use_id":"toolu_arrayoffset","is_error":false,` +
			`"content":"ok"}]}}`,
		"not json",
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	summary := runBackfill(t, store, []string{dir}, since)

	if len(summary.Malformed) != 1 || summary.Malformed[0].Line != 4 {
		t.Errorf("malformed = %+v, want only %s line 4", summary.Malformed, path)
	}
	if summary.CallsInserted != 2 {
		t.Errorf("calls inserted = %d, want 2", summary.CallsInserted)
	}
	for id, cause := range map[string]string{
		"toolu_nopath":      "Read input has no file_path",
		"toolu_arrayoffset": "decode Read input",
	} {
		c := call(t, store, id)
		if c.failed == nil || !*c.failed {
			t.Errorf("%s failed = %v, want true", id, c.failed)
		}
		if want := "malformed input: " + cause; !strings.HasPrefix(str(c.errText), want) {
			t.Errorf("%s error = %q, want prefix %q", id, str(c.errText), want)
		}
		if str(c.tool) != "Read" || str(c.request) != "msg_bad_input" || c.input == nil || c.ts == nil {
			t.Errorf("%s lost its entry columns: tool %s request %s input %s ts %s",
				id, str(c.tool), str(c.request), str(c.input), num(c.ts))
		}
		if n := count(t, store, "SELECT COUNT(*) FROM faults WHERE error LIKE ?", "%"+id+"%"); n != 0 {
			t.Errorf("faults naming %s = %d, want 0", id, n)
		}
	}
	if n := count(t, store, "SELECT COUNT(*) FROM faults WHERE stage = ? AND error LIKE ?",
		callmeter.StageBackfill, "%"+path+" line 4%"); n != 1 {
		t.Errorf("faults naming %s line 4 = %d, want 1", path, n)
	}
}

// TestRunCountsAFailedBashCallsOutput: a failed Bash call's toolUseResult is
// the plain string "Error: " + the text the hook's PostToolUseFailure carries
// as `error`; backfill sizes it the same, so both writers agree.
func TestRunCountsAFailedBashCallsOutput(t *testing.T) {
	store := openStore(t)
	dir := filepath.Join(t.TempDir(), ".claude")
	project := filepath.Join(dir, "projects", "-tmp-demo-proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("make project dir: %v", err)
	}
	output := `Exit code 1\nFAIL some_test\nFAIL other_test`
	lines := []string{
		`{"type":"assistant","timestamp":"2026-09-20T11:00:00.000Z","cwd":"/tmp/demo-proj","sessionId":"sess-fail",` +
			`"message":{"id":"msg_fail","content":[{"type":"tool_use","id":"toolu_failbash","name":"Bash",` +
			`"input":{"command":"go test ./..."}}],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"user","timestamp":"2026-09-20T11:00:01.000Z","cwd":"/tmp/demo-proj","sessionId":"sess-fail",` +
			`"message":{"content":[{"type":"tool_result","tool_use_id":"toolu_failbash","is_error":true,` +
			`"content":"` + output + `"}]},"toolUseResult":"Error: ` + output + `"}`,
	}
	path := filepath.Join(project, "sess-fail.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	runBackfill(t, store, []string{dir}, since)
	want := int64(len("Exit code 1\nFAIL some_test\nFAIL other_test"))
	if c := call(t, store, "toolu_failbash"); c.bytesReal == nil || *c.bytesReal != want {
		t.Errorf("bytes_real = %s, want %d (the error text without its \"Error: \" prefix)", num(c.bytesReal), want)
	}
}

// TestRunFillsAgentTotalsFromItsTranscript: a background or nested agent's
// parent result carries no totals (live: 2,238 of 2,246 backfilled agents had
// none), so backfill sums the agent's own transcript, as the hook does at
// SubagentStop (callmeter.ReadAgentTotals): one message's usage counts once.
func TestRunFillsAgentTotalsFromItsTranscript(t *testing.T) {
	store := openStore(t)
	dir := filepath.Join(t.TempDir(), ".claude")
	subagents := filepath.Join(dir, "projects", "-tmp-demo-proj", "sess-bg", "subagents")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatalf("make subagents dir: %v", err)
	}
	msg := func(id, ts, content string) string {
		return `{"type":"assistant","timestamp":"2026-09-20T11:00:0` + ts + `.000Z","cwd":"/tmp/demo-proj",` +
			`"sessionId":"sess-bg","message":{"id":"` + id + `","model":"claude-haiku-4-5","content":[` + content +
			`],"usage":{"input_tokens":10,"cache_read_input_tokens":20,"cache_creation_input_tokens":30,"output_tokens":5}}}`
	}
	lines := []string{
		msg("msg_bg1", "0", `{"type":"text","text":"looking"}`),
		msg("msg_bg1", "1", `{"type":"tool_use","id":"toolu_bg1","name":"Bash","input":{"command":"ls"}}`),
		msg("msg_bg2", "2", `{"type":"tool_use","id":"toolu_bg2","name":"Bash","input":{"command":"pwd"}}`),
	}
	path := filepath.Join(subagents, "agent-abg.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	runBackfill(t, store, []string{dir}, since)
	var total, uses int64
	var model string
	if err := store.DB().QueryRowContext(context.Background(),
		"SELECT total_tokens, tool_uses, model FROM agents WHERE agent_id = 'abg'").
		Scan(&total, &uses, &model); err != nil {
		t.Fatalf("read agent abg: %v", err)
	}
	if total != 130 || uses != 2 || model != "claude-haiku-4-5" {
		t.Errorf("agent abg = total %d, tool uses %d, model %q; want 130, 2, claude-haiku-4-5", total, uses, model)
	}
}

// TestRunAgentTotalsAreTheWholeTranscript: an agent woken for another turn
// (an orchestrator waiting on its children, a SendMessage) keeps writing to
// its transcript after its parent's Agent result was taken, so that result
// counts only the first turn (live: 86 tool uses stored as 10). The agent's
// own transcript is the total, the same sum the hook reads at SubagentStop;
// the result's resolved model stays.
func TestRunAgentTotalsAreTheWholeTranscript(t *testing.T) {
	store := openStore(t)
	dir := filepath.Join(t.TempDir(), ".claude")
	project := filepath.Join(dir, "projects", "-tmp-demo-proj")
	subagents := filepath.Join(project, "sess-wake", "subagents")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatalf("make subagents dir: %v", err)
	}
	at := func(sec int) string {
		return fmt.Sprintf(
			`"timestamp":"2026-09-20T12:00:0%d.000Z","cwd":"/tmp/demo-proj","sessionId":"sess-wake"`,
			sec,
		)
	}
	call := `{"type":"tool_use","id":"toolu_W","name":"Agent",` +
		`"input":{"description":"orchestrate","prompt":"run it","subagent_type":"general-purpose"}}`
	result := `"toolUseResult":{"status":"completed","agentId":"awake","agentType":"general-purpose",` +
		`"resolvedModel":"claude-opus-4-5","totalTokens":50,"totalToolUseCount":1}`
	parent := []string{
		`{"type":"assistant","message":{"model":"claude-opus-4-5","id":"msg_p1","content":[` + call + `],` +
			`"usage":{"input_tokens":1,"output_tokens":9}},` + at(0) + `}`,
		`{"type":"user","message":{"role":"user","content":[` +
			`{"tool_use_id":"toolu_W","type":"tool_result","content":"waiting"}]},` + at(1) + `,` + result + `}`,
	}
	msg := func(id string, sec int, block, stop string, tokens int) string {
		return `{"type":"assistant","message":{"model":"claude-sonnet-4-5","id":"` + id + `","content":[` + block +
			`],"stop_reason":"` + stop + `","usage":{"input_tokens":` + fmt.Sprint(tokens) + `,"output_tokens":0}},` +
			at(sec) + `}`
	}
	bash := func(id, command string) string {
		return `{"type":"tool_use","id":"` + id + `","name":"Bash","input":{"command":"` + command + `"}}`
	}
	agent := []string{
		msg("msg_w1", 2, bash("toolu_W1", "ls"), "tool_use", 30),
		msg("msg_w2", 3, `{"type":"text","text":"waiting"}`, "end_turn", 20),
		msg("msg_w3", 4, bash("toolu_W2", "pwd"), "tool_use", 400),
		msg("msg_w4", 5, `{"type":"text","text":"done"}`, "end_turn", 600),
	}
	write := func(path string, lines []string) {
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(filepath.Join(project, "sess-wake.jsonl"), parent)
	write(filepath.Join(subagents, "agent-awake.jsonl"), agent)
	runBackfill(t, store, []string{dir}, since)
	var total, uses int64
	var model string
	if err := store.DB().QueryRowContext(context.Background(),
		"SELECT total_tokens, tool_uses, model FROM agents WHERE agent_id = 'awake'").
		Scan(&total, &uses, &model); err != nil {
		t.Fatalf("read agent awake: %v", err)
	}
	if total != 30+20+400+600 || uses != 2 || model != "claude-opus-4-5" {
		t.Errorf("agent awake = total %d, tool uses %d, model %q; want 1050, 2, claude-opus-4-5", total, uses, model)
	}
}

// TestRunNestedAgentAndSilentAgent: Claude Code keeps every agent of a session
// in one flat subagents/ directory, so an agent spawned by a sub-agent is
// subagents/agent-{child}.jsonl, never under its parent's name (live: 19
// nested agents pointed at a path that does not exist, and lost their
// totals). An agent that never replied has 0 tokens, not unknown.
func TestRunNestedAgentAndSilentAgent(t *testing.T) {
	store := openStore(t)
	dir := filepath.Join(t.TempDir(), ".claude")
	subagents := filepath.Join(dir, "projects", "-tmp-demo-proj", "sess-n", "subagents")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatalf("make subagents dir: %v", err)
	}
	head := `"cwd":"/tmp/demo-proj","sessionId":"sess-n",`
	parent := []string{
		`{"type":"assistant","timestamp":"2026-09-20T11:00:00.000Z",` + head + `"message":{"id":"msg_p1",` +
			`"content":[{"type":"tool_use","id":"toolu_spawn","name":"Agent","input":{"prompt":"go"}},` +
			`{"type":"tool_use","id":"toolu_spawn2","name":"Agent","input":{"prompt":"go"}}],` +
			`"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"user","timestamp":"2026-09-20T11:00:05.000Z",` + head + `"message":{"content":[` +
			`{"type":"tool_result","tool_use_id":"toolu_spawn","content":"launched"}]},` +
			`"toolUseResult":{"status":"async_launched","agentId":"achild","agentType":"tracer"}}`,
		`{"type":"user","timestamp":"2026-09-20T11:00:06.000Z",` + head + `"message":{"content":[` +
			`{"type":"tool_result","tool_use_id":"toolu_spawn2","content":"launched"}]},` +
			`"toolUseResult":{"status":"async_launched","agentId":"asilent","agentType":"scribe"}}`,
	}
	child := []string{
		`{"type":"assistant","timestamp":"2026-09-20T11:00:02.000Z",` + head + `"message":{"id":"msg_c1",` +
			`"model":"claude-haiku-4-5","content":[{"type":"tool_use","id":"toolu_c1","name":"Bash",` +
			`"input":{"command":"ls"}}],"usage":{"input_tokens":1,"cache_read_input_tokens":2,` +
			`"cache_creation_input_tokens":3,"output_tokens":4}}}`,
	}
	silent := []string{`{"type":"user","timestamp":"2026-09-20T11:00:07.000Z",` + head +
		`"message":{"role":"user","content":"do it"}}`}
	files := map[string][]string{"agent-aspawner": parent, "agent-achild": child, "agent-asilent": silent}
	for name, lines := range files {
		body := []byte(strings.Join(lines, "\n") + "\n")
		if err := os.WriteFile(filepath.Join(subagents, name+".jsonl"), body, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	runBackfill(t, store, []string{dir}, since)
	for id, want := range map[string]struct {
		path        string
		total, uses int64
	}{
		"achild":  {filepath.Join(subagents, "agent-achild.jsonl"), 10, 1},
		"asilent": {filepath.Join(subagents, "agent-asilent.jsonl"), 0, 0},
	} {
		var path string
		var total, uses *int64
		if err := store.DB().QueryRowContext(context.Background(),
			"SELECT transcript_path, total_tokens, tool_uses FROM agents WHERE agent_id = ?", id).
			Scan(&path, &total, &uses); err != nil {
			t.Fatalf("read agent %s: %v", id, err)
		}
		if path != want.path || total == nil || *total != want.total || uses == nil || *uses != want.uses {
			t.Errorf("agent %s = path %s total %s uses %s; want %s %d %d",
				id, path, num(total), num(uses), want.path, want.total, want.uses)
		}
	}
}

// TestRunAgentTotalsNeverAddARowOutsideTheWindow: totals only fill a row the
// run wrote; a sub-agent file whose entries are all older than since leaves
// no row behind.
func TestRunAgentTotalsNeverAddARowOutsideTheWindow(t *testing.T) {
	store := openStore(t)
	dir := filepath.Join(t.TempDir(), ".claude")
	subagents := filepath.Join(dir, "projects", "-tmp-demo-proj", "sess-old", "subagents")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatalf("make subagents dir: %v", err)
	}
	line := `{"type":"assistant","timestamp":"2020-01-01T00:00:00.000Z","cwd":"/tmp/demo-proj","sessionId":"sess-old",` +
		`"message":{"id":"msg_old","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":1,"output_tokens":1}}}`
	if err := os.WriteFile(filepath.Join(subagents, "agent-aold.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	runBackfill(t, store, []string{dir}, since)
	if n := count(t, store, "SELECT COUNT(*) FROM agents WHERE agent_id = 'aold'"); n != 0 {
		t.Errorf("agent rows for an out-of-window sub-agent = %d, want 0", n)
	}
}

// TestRunResolvesAPendingRequestTheHookLeft: the hook keys a request
// pending:{tool_use_id} while its model message is not yet on disk, and only a
// later event of the same chat resolves it; an agent that stops while the hook
// is absent leaves it pending for good (live: 15 rows, every id on disk).
// Backfill reading the message resolves it as the hook's resolvePending does.
func TestRunResolvesAPendingRequestTheHookLeft(t *testing.T) {
	store := openStore(t)
	dir := filepath.Join(t.TempDir(), ".claude")
	project := filepath.Join(dir, "projects", "-tmp-demo-proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("make project dir: %v", err)
	}
	bash := func(id string) string {
		return `{"type":"tool_use","id":"` + id + `","name":"Bash","input":{"command":"ls"}}`
	}
	line := `{"type":"assistant","timestamp":"2026-09-20T13:00:00.000Z","cwd":"/tmp/demo-proj",` +
		`"sessionId":"sess-pend","message":{"id":"msg_P","content":[` + bash("toolu_P1") + `,` + bash("toolu_P2") +
		`],"usage":{"input_tokens":100,"cache_read_input_tokens":20,"cache_creation_input_tokens":3,"output_tokens":7}}}`
	if err := os.WriteFile(filepath.Join(project, "sess-pend.jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	ctx := context.Background()
	provisional := callmeter.ProvisionalKey("toolu_P1")
	err := store.Batch(ctx, func(tx *callmeter.Tx) error {
		if err := tx.UpsertRequest(ctx, callmeter.Request{
			RequestID: provisional,
			SessionID: callmeter.Ptr("sess-pend"),
			TS:        callmeter.Ptr(int64(1)),
			Calls:     callmeter.Ptr(int64(2)),
			Pending:   callmeter.Ptr(true),
			Source:    callmeter.Ptr(callmeter.SourceHook),
		}, callmeter.Overwrite); err != nil {
			return err
		}
		for _, id := range []string{"toolu_P1", "toolu_P2"} {
			if err := tx.UpsertCall(ctx, callmeter.Call{
				ToolUseID: id,
				SessionID: callmeter.Ptr("sess-pend"),
				RequestID: callmeter.Ptr(provisional),
				Tool:      callmeter.Ptr("Bash"),
				Source:    callmeter.Ptr(callmeter.SourceHook),
			}, callmeter.Overwrite); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed hook rows: %v", err)
	}
	runBackfill(t, store, []string{dir}, since)
	for _, id := range []string{"toolu_P1", "toolu_P2"} {
		if got := str(call(t, store, id).request); got != "msg_P" {
			t.Errorf("call %s request = %s, want msg_P", id, got)
		}
	}
	if n := count(t, store, "SELECT COUNT(*) FROM requests WHERE request_id LIKE 'pending:%' OR pending = 1"); n != 0 {
		t.Errorf("%d pending requests remain, want 0", n)
	}
	var contextTokens, calls int64
	if err := store.DB().QueryRowContext(ctx,
		"SELECT context_tokens, calls FROM requests WHERE request_id = 'msg_P'").
		Scan(&contextTokens, &calls); err != nil {
		t.Fatalf("read request msg_P: %v", err)
	}
	if contextTokens != 123 || calls != 2 {
		t.Errorf("msg_P context=%d calls=%d, want 123 and 2", contextTokens, calls)
	}
	if n := count(t, store, "SELECT COUNT(*) FROM requests"); n != 1 {
		t.Errorf("%d request rows, want 1", n)
	}
}

// TestRunSetsStoppedFromAFinalTranscript: backfill never set agents.stopped
// (live: 2195 agents NULL). A transcript whose last assistant entry ends the
// turn (callmeter.AgentTotals.Final) stopped at that entry; one still running
// or killed mid-turn has no stop; a hook's stop is never overwritten.
func TestRunSetsStoppedFromAFinalTranscript(t *testing.T) {
	store := openStore(t)
	dir := filepath.Join(t.TempDir(), ".claude")
	subagents := filepath.Join(dir, "projects", "-tmp-demo-proj", "sess-stop", "subagents")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatalf("make subagents dir: %v", err)
	}
	msg := func(id string, sec int, block, stop string) string {
		return fmt.Sprintf(`{"type":"assistant","timestamp":"2026-09-20T14:00:0%d.000Z","cwd":"/tmp/demo-proj",`+
			`"sessionId":"sess-stop","message":{"id":"%s","model":"claude-haiku-4-5","content":[%s],`+
			`"stop_reason":"%s","usage":{"input_tokens":1,"output_tokens":1}}}`, sec, id, block, stop)
	}
	bash := func(id string) string {
		return `{"type":"tool_use","id":"` + id + `","name":"Bash","input":{"command":"ls"}}`
	}
	done := `{"type":"text","text":"done"}`
	transcripts := map[string][]string{
		"afinal":  {msg("msg_f1", 0, bash("toolu_F1"), "tool_use"), msg("msg_f2", 3, done, "end_turn")},
		"arun":    {msg("msg_r1", 0, bash("toolu_R1"), "tool_use")},
		"ahooked": {msg("msg_h1", 0, bash("toolu_H1"), "tool_use"), msg("msg_h2", 4, done, "end_turn")},
	}
	for id, lines := range transcripts {
		path := filepath.Join(subagents, "agent-"+id+".jsonl")
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	if err := store.UpsertAgent(context.Background(), callmeter.Agent{
		AgentID: "ahooked",
		Stopped: callmeter.Ptr(int64(42)),
		Source:  callmeter.Ptr(callmeter.SourceHook),
	}, callmeter.Overwrite); err != nil {
		t.Fatalf("seed hook agent: %v", err)
	}
	runBackfill(t, store, []string{dir}, since)
	want := map[string]string{
		"afinal":  fmt.Sprint(time.Date(2026, 9, 20, 14, 0, 3, 0, time.UTC).UnixMilli()),
		"arun":    "<NULL>",
		"ahooked": "42",
	}
	for id, stopped := range want {
		var got *int64
		if err := store.DB().QueryRowContext(context.Background(),
			"SELECT stopped FROM agents WHERE agent_id = ?", id).Scan(&got); err != nil {
			t.Fatalf("read agent %s: %v", id, err)
		}
		if num(got) != stopped {
			t.Errorf("agent %s stopped = %s, want %s", id, num(got), stopped)
		}
	}
}

// TestRunSetsStoppedFromATaskNotice: Claude Code writes stop_reason null on
// many finished sub-agent transcripts, so sumTotals's Final rule never fires
// for them (docs/design/hooks/callmeter.md). A background agent's real
// completion arrives later in its PARENT transcript, holding <task-id> and
// <status>: as a plain-string "user" entry when it lands in a turn, or —
// live: most of them — only ever as the "queue-operation" enqueue that
// never became one (a session that moved on before absorbing it).
func TestRunSetsStoppedFromATaskNotice(t *testing.T) {
	store := openStore(t)
	dir := filepath.Join(t.TempDir(), ".claude")
	subagents := filepath.Join(dir, "projects", "-tmp-demo-proj", "sess-notice", "subagents")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatalf("make subagents dir: %v", err)
	}
	head := `"cwd":"/tmp/demo-proj","sessionId":"sess-notice",`
	launch := func(toolID, agentID string) string {
		return `{"type":"user","timestamp":"2026-09-20T15:00:01.000Z",` + head + `"message":{"content":[` +
			`{"type":"tool_result","tool_use_id":"` + toolID + `","content":"launched"}]},` +
			`"toolUseResult":{"status":"async_launched","agentId":"` + agentID + `","agentType":"tracer"}}`
	}
	notice := func(sec int, id, status string) string {
		return fmt.Sprintf(`{"type":"user","timestamp":"2026-09-20T15:00:%02d.000Z",`+head+
			`"message":{"role":"user","content":"<task-notification><task-id>%s</task-id>`+
			`<status>%s</status></task-notification>"}}`, sec, id, status)
	}
	queued := func(sec int, id, status string) string {
		return fmt.Sprintf(`{"type":"queue-operation","operation":"enqueue",`+
			`"timestamp":"2026-09-20T15:00:%02d.000Z",`+head+
			`"content":"<task-notification><task-id>%s</task-id>`+
			`<status>%s</status></task-notification>"}`, sec, id, status)
	}
	parent := []string{
		`{"type":"assistant","timestamp":"2026-09-20T15:00:00.000Z",` + head + `"message":{"id":"msg_p1",` +
			`"content":[{"type":"tool_use","id":"toolu_c1","name":"Agent","input":{"prompt":"go"}},` +
			`{"type":"tool_use","id":"toolu_c2","name":"Agent","input":{"prompt":"go"}},` +
			`{"type":"tool_use","id":"toolu_c3","name":"Agent","input":{"prompt":"go"}},` +
			`{"type":"tool_use","id":"toolu_c4","name":"Agent","input":{"prompt":"go"}},` +
			`{"type":"tool_use","id":"toolu_c5","name":"Agent","input":{"prompt":"go"}}],` +
			`"usage":{"input_tokens":1,"output_tokens":1}}}`,
		launch("toolu_c1", "acompleted"),
		launch("toolu_c2", "akilled"),
		launch("toolu_c3", "anonotice"),
		launch("toolu_c4", "aalreadyhooked"),
		launch("toolu_c5", "aqueued"),
		notice(10, "acompleted", "completed"),
		notice(20, "akilled", "killed"),
		notice(30, "aalreadyhooked", "completed"),
		queued(15, "aqueued", "completed"),
	}
	unstopped := func(id string) []string {
		return []string{`{"type":"assistant","timestamp":"2026-09-20T15:00:02.000Z",` + head +
			`"message":{"id":"msg_` + id + `","model":"claude-haiku-4-5",` +
			`"content":[{"type":"tool_use","id":"toolu_` + id + `","name":"Bash","input":{"command":"ls"}}],` +
			`"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1}}}`}
	}
	files := map[string][]string{
		"agent-aspawnernotice": parent,
		"agent-acompleted":     unstopped("acompleted"),
		"agent-akilled":        unstopped("akilled"),
		"agent-anonotice":      unstopped("anonotice"),
		"agent-aalreadyhooked": unstopped("aalreadyhooked"),
		"agent-aqueued":        unstopped("aqueued"),
	}
	for name, lines := range files {
		body := []byte(strings.Join(lines, "\n") + "\n")
		if err := os.WriteFile(filepath.Join(subagents, name+".jsonl"), body, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := store.UpsertAgent(context.Background(), callmeter.Agent{
		AgentID: "aalreadyhooked",
		Stopped: callmeter.Ptr(int64(42)),
		Source:  callmeter.Ptr(callmeter.SourceHook),
	}, callmeter.Overwrite); err != nil {
		t.Fatalf("seed hook agent: %v", err)
	}
	runBackfill(t, store, []string{dir}, since)
	want := map[string]string{
		"acompleted":     fmt.Sprint(time.Date(2026, 9, 20, 15, 0, 10, 0, time.UTC).UnixMilli()),
		"akilled":        fmt.Sprint(time.Date(2026, 9, 20, 15, 0, 20, 0, time.UTC).UnixMilli()),
		"anonotice":      "<NULL>",
		"aalreadyhooked": "42",
		"aqueued":        fmt.Sprint(time.Date(2026, 9, 20, 15, 0, 15, 0, time.UTC).UnixMilli()),
	}
	for id, stopped := range want {
		var got *int64
		if err := store.DB().QueryRowContext(context.Background(),
			"SELECT stopped FROM agents WHERE agent_id = ?", id).Scan(&got); err != nil {
			t.Fatalf("read agent %s: %v", id, err)
		}
		if num(got) != stopped {
			t.Errorf("agent %s stopped = %s, want %s", id, num(got), stopped)
		}
	}
}
