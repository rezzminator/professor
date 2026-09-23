package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
	"github.com/rezzminator/professor/pfm/internal/callmeter/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/callmeter/report"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// The captured payloads (testdata/callmeter/*.jsonl) name these neutral
// roots; a lab rewrites them to its own temp directory.
const (
	cmDemoHome  = "/tmp/demo-home"
	cmDemoProj  = "/tmp/demo-proj"
	cmSessionA  = "b2c7b094-91c1-4b76-8621-258b240b695a"
	cmSessionB  = "406b5ade-ce88-4f9f-8ea1-7e9aa4fd2a31"
	cmSubagent  = "a06aef038839fab57"
	cmEchoCall  = "toolu_01MMa5wYFxvUL9L8JdPvB3hV"
	cmBatchLead = "toolu_01UGaJs715PJE1hKtJWM2ANC"
)

type callmeterLab struct {
	t         *testing.T
	ctx       context.Context
	rec       *obs.Recorder
	root      string
	home      string
	proj      string
	storePath string
	clock     *clock.Fake
	store     *callmeter.Store
}

func newCallmeterLab(t *testing.T) *callmeterLab {
	t.Helper()
	root := t.TempDir()
	lab := &callmeterLab{
		t:         t,
		root:      root,
		home:      filepath.Join(root, "demo-home"),
		proj:      filepath.Join(root, "demo-proj"),
		storePath: filepath.Join(root, "state", "callmeter.db"),
		clock:     clock.NewFake(time.Date(2026, 9, 23, 1, 30, 0, 0, time.UTC)),
	}
	lab.ctx, lab.rec = obs.Test(t)
	source := filepath.Join("testdata", "callmeter", "demo-home")
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(lab.home, strings.TrimPrefix(path, source))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy transcript fixtures: %v", err)
	}
	if err := os.MkdirAll(lab.proj, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	return lab
}

// payloads reads one captured fixture, every neutral root rewritten to the lab's.
func (lab *callmeterLab) payloads(name string) []string {
	lab.t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "callmeter", name))
	if err != nil {
		lab.t.Fatalf("read fixture %s: %v", name, err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.ReplaceAll(line, cmDemoHome, lab.home)
		lines = append(lines, strings.ReplaceAll(line, cmDemoProj, lab.proj))
	}
	return lines
}

func (lab *callmeterLab) feed(payloads ...string) {
	lab.t.Helper()
	for _, payload := range payloads {
		var stderr bytes.Buffer
		if code := runCallmeter(lab.ctx, strings.NewReader(payload), &stderr, lab.storePath, lab.clock); code != 0 {
			lab.t.Fatalf("exit code = %d, want 0 on every path; stderr = %q", code, stderr.String())
		}
	}
}

func (lab *callmeterLab) transcript(session string) string {
	return filepath.Join(lab.home, ".claude", "projects", "-tmp-demo-proj", session+".jsonl")
}

// truncate keeps the first n lines of a transcript: the later requests are
// "not on disk yet". It returns the full content for restore.
func (lab *callmeterLab) truncate(path string, n int) []byte {
	lab.t.Helper()
	full, err := os.ReadFile(path)
	if err != nil {
		lab.t.Fatalf("read transcript: %v", err)
	}
	lines := strings.SplitAfter(string(full), "\n")
	if err := os.WriteFile(path, []byte(strings.Join(lines[:n], "")), 0o644); err != nil {
		lab.t.Fatalf("truncate transcript: %v", err)
	}
	return full
}

func (lab *callmeterLab) write(path string, data []byte) {
	lab.t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		lab.t.Fatalf("write %s: %v", path, err)
	}
}

func (lab *callmeterLab) db() *callmeter.Store {
	lab.t.Helper()
	if lab.store == nil {
		store, err := callmeter.OpenDB(context.Background(), lab.storePath)
		if err != nil {
			lab.t.Fatalf("open store: %v", err)
		}
		lab.t.Cleanup(func() {
			if err := store.Close(); err != nil {
				lab.t.Errorf("close store: %v", err)
			}
		})
		lab.store = store
	}
	return lab.store
}

// row reads one row as column -> fmt.Sprint(value); NULL reads "<nil>".
func (lab *callmeterLab) row(query string, args ...any) map[string]string {
	lab.t.Helper()
	rows, err := lab.db().DB().QueryContext(context.Background(), query, args...)
	if err != nil {
		lab.t.Fatalf("query %q: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		lab.t.Fatalf("columns of %q: %v", query, err)
	}
	if !rows.Next() {
		lab.t.Fatalf("query %q %v: no row (err %v)", query, args, rows.Err())
	}
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	if err := rows.Scan(pointers...); err != nil {
		lab.t.Fatalf("scan %q: %v", query, err)
	}
	out := map[string]string{}
	for i, name := range columns {
		out[name] = fmt.Sprint(values[i])
	}
	return out
}

func (lab *callmeterLab) count(query string, args ...any) int {
	lab.t.Helper()
	n, err := func() (int, error) {
		var n int
		err := lab.db().DB().QueryRowContext(context.Background(), query, args...).Scan(&n)
		return n, err
	}()
	if err != nil {
		lab.t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func (lab *callmeterLab) call(id string) map[string]string {
	lab.t.Helper()
	return lab.row("SELECT * FROM calls WHERE tool_use_id = ?", id)
}

func expect(t *testing.T, what string, row map[string]string, want map[string]any) {
	t.Helper()
	for column, value := range want {
		if got := row[column]; got != fmt.Sprint(value) {
			t.Errorf("%s.%s = %q, want %q", what, column, got, fmt.Sprint(value))
		}
	}
}

// payloadField digs one value out of a raw payload line by its key path.
func payloadField(t *testing.T, line string, path ...string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(line), &value); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	for _, key := range path {
		switch node := value.(type) {
		case map[string]any:
			value = node[key]
		case []any:
			var index int
			if _, err := fmt.Sscan(key, &index); err != nil {
				t.Fatalf("index %q: %v", key, err)
			}
			value = node[index]
		default:
			t.Fatalf("payload path %v: %q is not a container", path, key)
		}
	}
	return value
}

func TestCallmeterCapturedPayloads(t *testing.T) {
	lab := newCallmeterLab(t)
	configDir, err := filepath.EvalSymlinks(filepath.Join(lab.home, ".claude"))
	if err != nil {
		t.Fatalf("resolve config dir: %v", err)
	}
	fixture := strings.Repeat("package demo\n", 100)
	lab.write(filepath.Join(lab.proj, "fixture.go"), []byte(fixture))

	// The chat and its sub-agent. The capture predates PostToolBatch, so the
	// sub-agent's two batches are built here in the harness's shape; the
	// second lands before its request reaches the sub-agent transcript.
	scripted := lab.payloads("scripted.jsonl")
	subTranscript := callmeter.SubagentTranscriptPath(lab.transcript(cmSessionA), cmSubagent)
	subBatch := func(id, response string) string {
		return fmt.Sprintf(
			`{"session_id":%q,"transcript_path":%q,"cwd":%q,"agent_id":%q,"agent_type":"general-purpose",`+
				`"hook_event_name":"PostToolBatch","tool_calls":[{"tool_name":"Read","tool_input":{},"tool_use_id":%q,"tool_response":%s}]}`,
			cmSessionA, lab.transcript(cmSessionA), lab.proj, cmSubagent, id, response)
	}
	lab.feed(scripted[:7]...)
	lab.feed(subBatch("toolu_01V8dv8UQGCZREurDdztPdMd", `[{"type":"text","text":"abc"},{"type":"text","text":"defg"}]`))
	lab.feed(scripted[7])
	full := lab.truncate(subTranscript, 3)
	lab.feed(subBatch("toolu_01PjfJjL3jCooHMyVrkf8UxA", `"151 fixture.go"`))
	expect(t, "sub-agent pending request", lab.row(
		"SELECT * FROM requests WHERE request_id = ?", callmeter.ProvisionalKey("toolu_01PjfJjL3jCooHMyVrkf8UxA"),
	), map[string]any{"pending": 1, "agent_id": cmSubagent, "calls": 1})
	lab.write(subTranscript, full)
	lab.feed(scripted[8:]...)

	readContent, _ := payloadField(t, scripted[0], "tool_response", "file", "content").(string)
	expect(t, "whole Read", lab.call("toolu_0184pmUECyYH9FvhUrZmgYSB"), map[string]any{
		"tool": "Read", "failed": 0, "bytes_real": len(readContent), "file_path": filepath.Join(lab.proj, "fixture.go"),
		"file_bytes": len(fixture), "read_start": 1, "read_lines": 152, "read_total_lines": 152,
		"session_id": cmSessionA, "agent_id": nil, "cwd": lab.proj, "source": callmeter.SourceHook,
		"config_dir": configDir, "ts": lab.clock.Now().UnixMilli(), "duration_ms": 2,
	})
	expect(t, "ranged Read", lab.call("toolu_01BQAycMmK5m23hu7UWYnSHr"), map[string]any{
		"read_start": 20, "read_lines": 15, "read_total_lines": 152,
	})
	write := lab.call("toolu_01T4ttjYv9CKMKP9fdobnE3Y")
	expect(t, "Write", write, map[string]any{"tool": "Write", "file_bytes": nil, "file_bytes_before": 0})
	if strings.Contains(write["input"], `"content":`) || !strings.Contains(write["input"], `"content_bytes":14`) {
		t.Errorf("Write input = %s, want content replaced by content_bytes", write["input"])
	}
	expect(
		t,
		"Edit",
		lab.call("toolu_01TM3J5vmCgPRboMQG6spZnF"),
		map[string]any{"tool": "Edit", "file_bytes_before": 14},
	)
	failure := lab.call("toolu_0159VRFgcqEF4RCnEtEF1gjm")
	expect(t, "failed Bash", failure,
		map[string]any{"tool": "Bash", "failed": 1, "bytes_real": len(failure["error"]), "duration_ms": 11})
	if !strings.HasPrefix(failure["error"], "Exit code 1\ncat: does-not-exist.txt") {
		t.Errorf("failed Bash error = %q", failure["error"])
	}
	expect(t, "sub-agent Read", lab.call("toolu_01V8dv8UQGCZREurDdztPdMd"), map[string]any{
		"agent_id": cmSubagent, "agent_type": "general-purpose", "bytes_delivered": 7, "request_id": "msg_demo_S1",
	})
	expect(t, "sub-agent Bash", lab.call("toolu_01PjfJjL3jCooHMyVrkf8UxA"), map[string]any{
		"agent_id": cmSubagent, "bytes_delivered": 14, "request_id": "msg_demo_S2",
	})
	expect(
		t,
		"resolved sub-agent request",
		lab.row("SELECT * FROM requests WHERE request_id = 'msg_demo_S2'"),
		map[string]any{
			"pending":        0,
			"agent_id":       cmSubagent,
			"context_tokens": 6 + 5390 + 2100,
			"output_tokens":  45,
			"calls":          1,
		},
	)
	expect(t, "Agent call", lab.call("toolu_01WWJxCy1xfH6wrvmTc7oF5c"), map[string]any{
		"tool": "Agent", "bytes_real": 3, "duration_ms": 4365,
	})
	agentTranscript, _ := payloadField(t, scripted[8], "agent_transcript_path").(string)
	expect(t, "agent", lab.row("SELECT * FROM agents WHERE agent_id = ?", cmSubagent), map[string]any{
		"parent_tool_use_id": "toolu_01WWJxCy1xfH6wrvmTc7oF5c",
		"agent_type":         "general-purpose",
		"session_id":         cmSessionA,
		"total_tokens":       18107,
		"tool_uses":          2,
		"model":              "claude-haiku-4-5-20251001",
		"started":            lab.clock.Now().UnixMilli(),
		"stopped":            lab.clock.Now().UnixMilli(),
		"transcript_path":    agentTranscript,
		"config_dir":         configDir,
	})

	// Three parallel calls in one batch, then one call whose request is not
	// on disk yet: pending until the Stop.
	probe5 := lab.payloads("probe5.jsonl")
	fullB := lab.truncate(lab.transcript(cmSessionB), 7)
	lab.feed(probe5[:6]...)
	expect(t, "echo call before Stop", lab.call(cmEchoCall), map[string]any{
		"request_id": callmeter.ProvisionalKey(cmEchoCall), "bytes_delivered": 3, "bytes_real": 3,
	})
	expect(
		t,
		"pending request",
		lab.row("SELECT * FROM requests WHERE request_id = ?", callmeter.ProvisionalKey(cmEchoCall)),
		map[string]any{"pending": 1, "calls": 1, "agent_id": nil, "session_id": cmSessionB},
	)
	lab.write(lab.transcript(cmSessionB), fullB)
	lab.feed(probe5[6])
	expect(t, "three-call request", lab.row("SELECT * FROM requests WHERE request_id = 'msg_demo_B1'"),
		map[string]any{"pending": 0, "calls": 3, "context_tokens": 4 + 11200 + 1850, "output_tokens": 210})
	for id, delivered := range map[string]int{
		cmBatchLead: 17, "toolu_014w33S7y4Hmv2iQj3NzzEWV": 19, "toolu_01LV57SCFxiU1LaMWZm3ixg6": 17,
	} {
		expect(
			t,
			"batched call "+id,
			lab.call(id),
			map[string]any{"request_id": "msg_demo_B1", "bytes_delivered": delivered},
		)
	}
	expect(t, "echo call after Stop", lab.call(cmEchoCall), map[string]any{"request_id": "msg_demo_B2"})
	expect(t, "Stop-resolved request", lab.row("SELECT * FROM requests WHERE request_id = 'msg_demo_B2'"),
		map[string]any{"pending": 0, "calls": 1, "context_tokens": 2 + 13050 + 120})

	// seq 1 7000: real 33,893 bytes, persisted, delivered as the preview.
	probe6 := lab.payloads("probe6.jsonl")
	lab.feed(probe6...)
	preview, _ := payloadField(t, probe6[2], "tool_calls", "0", "tool_response").(string)
	persisted, _ := payloadField(t, probe6[0], "tool_response", "persistedOutputPath").(string)
	if !strings.HasPrefix(preview, "<persisted-output>") {
		t.Fatalf("fixture drifted: seq batch response = %.40q", preview)
	}
	expect(t, "persisted Bash", lab.call("toolu_01CpbGnnWxfXpSqFSVgNHY34"), map[string]any{
		"bytes_real": 33893, "bytes_delivered": len(preview), "persisted_path": persisted, "request_id": "msg_demo_C1",
	})
	expect(
		t,
		"Read limit 5",
		lab.call("toolu_01WDSsG632Trh15WvoNiAPZd"),
		map[string]any{"read_lines": 5, "request_id": "msg_demo_C1"},
	)

	if n := lab.count("SELECT count(*) FROM faults"); n != 0 {
		t.Errorf("faults = %d, want 0 for the captured run", n)
	}
	if n := lab.count("SELECT count(*) FROM requests WHERE pending = 1"); n != 0 {
		t.Errorf("pending requests = %d, want 0 after every Stop", n)
	}
}

func TestCallmeterBatchBeforePostToolUse(t *testing.T) {
	rows := make([]map[string]string, 0, 2)
	for _, batchFirst := range []bool{false, true} {
		lab := newCallmeterLab(t)
		probe5 := lab.payloads("probe5.jsonl")
		use, batch := probe5[4], probe5[5]
		if batchFirst {
			use, batch = batch, use
		}
		lab.feed(use, batch)
		row := lab.call(cmEchoCall)
		for column, value := range row {
			row[column] = strings.ReplaceAll(value, lab.root, "{lab}") // each lab has its own temp root
		}
		rows = append(rows, row)
	}
	expect(t, "echo call", rows[0], map[string]any{"bytes_real": 3, "bytes_delivered": 3, "request_id": "msg_demo_B2"})
	for column, value := range rows[0] {
		if rows[1][column] != value {
			t.Errorf("column %s: PostToolUse first = %q, PostToolBatch first = %q", column, value, rows[1][column])
		}
	}
}

// TestCallmeterHookRowsReachTheReports: the reports read only what the hook
// stored. probe5 fed whole records its Bash calls; EnsureParsed parses every
// one of them, and fixture.go's BASH BYTES is the delivered bytes of the two
// calls that read it (each credits that one file, so its share is all of it).
func TestCallmeterHookRowsReachTheReports(t *testing.T) {
	lab := newCallmeterLab(t)
	fixture := filepath.Join(lab.proj, "fixture.go")
	lab.write(fixture, []byte(strings.Repeat("package demo\n", 100)))
	lab.feed(lab.payloads("probe5.jsonl")...)
	store := lab.db()
	bash := lab.count("SELECT COUNT(*) FROM calls WHERE tool = 'Bash'")
	if bash != 3 {
		t.Fatalf("the hook stored %d Bash calls, want probe5's 3", bash)
	}
	summary, err := report.EnsureParsed(lab.ctx, store, lab.home, nil)
	if err != nil {
		t.Fatalf("EnsureParsed: %v", err)
	}
	if summary.Parsed != bash || summary.SkippedRelative != 0 || summary.SkippedNoInput != 0 {
		t.Fatalf("EnsureParsed = %+v, want all %d hook-recorded Bash calls parsed", summary, bash)
	}
	if unparsed := lab.count(`SELECT COUNT(*) FROM calls c WHERE c.tool = 'Bash' AND NOT EXISTS
		(SELECT 1 FROM command_parts p WHERE p.tool_use_id = c.tool_use_id AND p.parser = ?)`, cmdparse.Version); unparsed != 0 {
		t.Fatalf("%d Bash calls have no parts from this parser", unparsed)
	}
	var want int64
	for _, id := range []string{"toolu_014w33S7y4Hmv2iQj3NzzEWV", "toolu_01LV57SCFxiU1LaMWZm3ixg6"} {
		stored := lab.call(id)["bytes_delivered"]
		delivered, err := strconv.ParseInt(stored, 10, 64)
		if err != nil || delivered == 0 {
			t.Fatalf("call %s bytes_delivered = %q (%v), want the hook's count", id, stored, err)
		}
		want += delivered
	}
	table, err := report.Files(lab.ctx, store, report.Filter{}, nil)
	if err != nil {
		t.Fatalf("report.Files: %v", err)
	}
	column := map[string]int{}
	for i, name := range table.Header {
		column[name] = i
	}
	for _, row := range table.Rows {
		if row[column["FILE"]] != fixture {
			continue
		}
		if got := row[column["BASH BYTES"]]; got != strconv.FormatInt(want, 10) {
			t.Fatalf("fixture.go BASH BYTES = %s, want %d: the hook's bytes of the two calls that read it", got, want)
		}
		return
	}
	t.Fatalf("report.Files has no row for %s: %v", fixture, table.Rows)
}

func TestCallmeterGarbagePayload(t *testing.T) {
	for name, payload := range map[string]string{
		"unparsable":    `{"hook_event_name": "PostToolUse", "tool_use_id": `,
		"unknown event": `{"hook_event_name": "SessionStart", "session_id": "s1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			lab := newCallmeterLab(t)
			lab.feed(payload)
			if n := lab.count("SELECT count(*) FROM faults WHERE stage = ?", callmeter.StagePayload); n != 1 {
				t.Errorf("payload faults = %d, want 1", n)
			}
			if n := lab.count("SELECT count(*) FROM faults"); n != 1 {
				t.Errorf("faults = %d, want exactly the one payload fault", n)
			}
			if n := lab.count("SELECT count(*) FROM calls"); n != 0 {
				t.Errorf("calls = %d, want 0", n)
			}
			assertLogged(t, lab.rec, callmeter.StagePayload)
		})
	}
}

func TestCallmeterUnreadableTranscript(t *testing.T) {
	lab := newCallmeterLab(t)
	// Malformed and holding the batch lead: the line the lookup must read. A
	// malformed line holding no wanted id is never decoded (callmeter.FindRequests).
	lab.write(lab.transcript(cmSessionB), []byte("this line is not JSON, yet names "+cmBatchLead+"\n"))
	probe5 := lab.payloads("probe5.jsonl")
	lab.feed(probe5[3])
	key := callmeter.ProvisionalKey(cmBatchLead)
	expect(t, "provisional request", lab.row("SELECT * FROM requests WHERE request_id = ?", key),
		map[string]any{"pending": 1, "calls": 3, "session_id": cmSessionB})
	expect(
		t,
		"batched call",
		lab.call("toolu_01LV57SCFxiU1LaMWZm3ixg6"),
		map[string]any{"request_id": key, "bytes_delivered": 17},
	)
	if n := lab.count(
		"SELECT count(*) FROM faults WHERE stage = ? AND tool_use_id = ?",
		callmeter.StageTranscript,
		cmBatchLead,
	); n != 1 {
		t.Errorf("transcript faults = %d, want 1", n)
	}
	assertLogged(t, lab.rec, callmeter.StageTranscript)
}

func TestCallmeterStoreUnopenable(t *testing.T) {
	lab := newCallmeterLab(t)
	blocker := filepath.Join(lab.root, "blocker")
	lab.write(blocker, []byte("a file where the store's directory should be"))
	lab.storePath = filepath.Join(blocker, "callmeter.db")
	var stderr bytes.Buffer
	if code := runCallmeter(
		lab.ctx,
		strings.NewReader(lab.payloads("probe5.jsonl")[0]),
		&stderr,
		lab.storePath,
		lab.clock,
	); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "toolu_01UGaJs715PJE1hKtJWM2ANC") {
		t.Errorf("stderr = %q, want the failure naming the call", stderr.String())
	}
	assertLogged(t, lab.rec, callmeter.StageStore)
}

func TestCallmeterEntryUsesHome(t *testing.T) {
	lab := newCallmeterLab(t)
	var stderr bytes.Buffer
	payload := lab.payloads("probe5.jsonl")[1]
	// PFM_HOME is the jail: the store must land under it, never under the OS
	// home the env also reports — a hook that ignored PFM_HOME wrote a fence
	// or test run's rows into the operator's real store.
	osHome := t.TempDir()
	jailed := &paths.MapEnv{HomeDir: osHome, Values: map[string]string{paths.EnvHome: lab.root}}
	if code := Callmeter(strings.NewReader(payload), &stderr, jailed); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(callmeter.DefaultPath(osHome)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("store written under the OS home %s despite PFM_HOME (stat err %v)", osHome, err)
	}
	lab.storePath = callmeter.DefaultPath(lab.root)
	expect(t, "Bash call", lab.call("toolu_014w33S7y4Hmv2iQj3NzzEWV"), map[string]any{"tool": "Bash", "bytes_real": 19})

	stderr.Reset()
	homeErr := errors.New("home unavailable")
	realHome := &paths.MapEnv{HomeErr: homeErr, Values: map[string]string{paths.EnvRealHome: "1"}}
	if code := Callmeter(strings.NewReader(payload), &stderr, realHome); code != 0 {
		t.Fatalf("exit code = %d, want 0 with no home", code)
	}
	if !strings.Contains(stderr.String(), "home unavailable") {
		t.Errorf("stderr = %q, want the home error said", stderr.String())
	}
}

func assertLogged(t *testing.T, rec *obs.Recorder, stage string) {
	t.Helper()
	for _, record := range rec.Records() {
		if value, ok := record.Field("step"); ok && value == stage && record.Level == "ERROR" {
			if _, ok := record.Field("session"); !ok {
				t.Errorf("log record %v carries no session", record.Fields)
			}
			return
		}
	}
	t.Errorf("no ERROR log record with step=%s; records: %v", stage, rec.Records())
}

// Two async hooks of one tool call (its PostToolUse and the PostToolBatch)
// fire together: the store open of one must wait out the other's write, never
// lose its whole record. A store open that wrote the schema version in a
// deferred transaction failed at once with SQLITE_BUSY, no busy wait.
func TestCallmeterStoreOpenWaitsOutAConcurrentWriter(t *testing.T) {
	lab := newCallmeterLab(t)
	probe5 := lab.payloads("probe5.jsonl")
	lab.feed(probe5[0]) // the first hook of the session created the store
	conn, err := lab.db().DB().Conn(context.Background())
	if err != nil {
		t.Fatalf("take a writer connection: %v", err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold the write lock: %v", err)
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(300 * time.Millisecond)
		if _, err := conn.ExecContext(context.Background(), "COMMIT"); err != nil {
			t.Errorf("release the write lock: %v", err)
		}
		if err := conn.Close(); err != nil {
			t.Errorf("close the writer connection: %v", err)
		}
	}()
	lab.feed(probe5[3])
	<-released
	expect(t, "batched call", lab.call("toolu_01LV57SCFxiU1LaMWZm3ixg6"), map[string]any{
		"request_id": "msg_demo_B1", "bytes_delivered": 17,
	})
	if n := lab.count("SELECT count(*) FROM faults"); n != 0 {
		t.Errorf("faults = %d, want 0", n)
	}
}

// The first hooks of a session race to create the store: the one that loses
// the switch to WAL must wait and retry, never lose its record.
func TestCallmeterFirstStoreOpenWaitsOutARacingCreator(t *testing.T) {
	lab := newCallmeterLab(t)
	if err := os.MkdirAll(filepath.Dir(lab.storePath), 0o700); err != nil {
		t.Fatalf("create store dir: %v", err)
	}
	creator, err := sqlitedb.OpenReadWrite(lab.storePath, time.Second)
	if err != nil {
		t.Fatalf("open the racing creator: %v", err)
	}
	t.Cleanup(func() {
		if err := creator.Close(); err != nil {
			t.Errorf("close the racing creator: %v", err)
		}
	})
	ctx := context.Background()
	if _, err := creator.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold the brand-new store's write lock: %v", err)
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(300 * time.Millisecond)
		if _, err := creator.ExecContext(ctx, "COMMIT"); err != nil {
			t.Errorf("release the write lock: %v", err)
		}
	}()
	lab.feed(lab.payloads("probe5.jsonl")[3])
	<-released
	expect(t, "batched call", lab.call("toolu_01LV57SCFxiU1LaMWZm3ixg6"), map[string]any{
		"request_id": "msg_demo_B1", "bytes_delivered": 17,
	})
	if n := lab.count("SELECT count(*) FROM faults"); n != 0 {
		t.Errorf("faults = %d, want 0", n)
	}
}

// asyncAgentResult is the Agent call of scripted.jsonl as a background launch
// returns it: no totals, only the agent id and its resolved model.
func asyncAgentResult(t *testing.T, payload string) string {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		t.Fatalf("decode Agent payload: %v", err)
	}
	fields["tool_response"] = map[string]any{
		"status":        "async_launched",
		"isAsync":       true,
		"agentId":       cmSubagent,
		"resolvedModel": "claude-haiku-4-5-20251001",
	}
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode Agent payload: %v", err)
	}
	return string(out)
}

func TestCallmeterSubagentStopFillsTotals(t *testing.T) {
	agentRow := func(lab *callmeterLab) map[string]string {
		return lab.row("SELECT * FROM agents WHERE agent_id = ?", cmSubagent)
	}
	t.Run("background agent", func(t *testing.T) {
		lab := newCallmeterLab(t)
		scripted := lab.payloads("scripted.jsonl")
		lab.feed(scripted[5], asyncAgentResult(t, scripted[9]), scripted[8])
		// The final message msg_demo_S3 is split across two entries, each
		// repeating its usage: it counts once, at its last entry's count.
		expect(t, "background agent", agentRow(lab), map[string]any{
			"total_tokens": (8 + 0 + 5390 + 60) + (6 + 5390 + 2100 + 45) + (2 + 4990 + 96 + 20),
			"tool_uses":    2,
			"model":        "claude-haiku-4-5-20251001", // the Agent result's, never the transcript's
		})
		if n := lab.count("SELECT count(*) FROM faults"); n != 0 {
			t.Errorf("faults = %d, want 0", n)
		}
	})
	t.Run("agent result first", func(t *testing.T) {
		lab := newCallmeterLab(t)
		scripted := lab.payloads("scripted.jsonl")
		lab.feed(scripted[5], scripted[9], scripted[8])
		expect(t, "foreground agent", agentRow(lab), map[string]any{
			"total_tokens": 18107, "tool_uses": 2, "model": "claude-haiku-4-5-20251001",
		})
	})
	t.Run("unreadable agent transcript", func(t *testing.T) {
		lab := newCallmeterLab(t)
		scripted := lab.payloads("scripted.jsonl")
		agentTranscript, _ := payloadField(t, scripted[8], "agent_transcript_path").(string)
		lab.write(agentTranscript, []byte("this line is not JSON\n"))
		lab.feed(scripted[5], scripted[8])
		expect(t, "agent", agentRow(lab), map[string]any{
			"total_tokens": nil, "stopped": lab.clock.Now().UnixMilli(),
		})
		if n := lab.count("SELECT count(*) FROM faults WHERE stage = ?", callmeter.StageTranscript); n != 1 {
			t.Errorf("transcript faults = %d, want 1", n)
		}
		assertLogged(t, lab.rec, callmeter.StageTranscript)
	})
}

// TestCallmeterSubagentStopWaitsForTheFinalMessage: Claude Code fires
// SubagentStop 20-50 ms after the agent's final message is stamped but before
// that line is flushed to its transcript (live: 31 of 33 agents summed without
// their final message). The hook re-reads until the last assistant entry
// carries a final stop_reason, so the totals hold that message.
func TestCallmeterSubagentStopWaitsForTheFinalMessage(t *testing.T) {
	lab := newCallmeterLab(t)
	scripted := lab.payloads("scripted.jsonl")
	agentTranscript, _ := payloadField(t, scripted[8], "agent_transcript_path").(string)
	full, err := os.ReadFile(agentTranscript)
	if err != nil {
		t.Fatalf("read agent transcript: %v", err)
	}
	cut := bytes.Index(full, []byte(`"uuid":"s-a3"`))
	cut = bytes.LastIndexByte(full[:cut], '\n') + 1
	lab.write(agentTranscript, full[:cut])
	flushed := make(chan struct{})
	go func() {
		defer close(flushed)
		time.Sleep(150 * time.Millisecond)
		if err := os.WriteFile(agentTranscript, full, 0o644); err != nil {
			t.Errorf("flush the final message: %v", err)
		}
	}()
	lab.feed(scripted[5], asyncAgentResult(t, scripted[9]), scripted[8])
	<-flushed
	expect(t, "agent", lab.row("SELECT * FROM agents WHERE agent_id = ?", cmSubagent), map[string]any{
		"total_tokens": 18107, "tool_uses": 2,
	})
}

// TestCallmeterResumedAgentKeepsItsFirstStartAndLatestTotals: an agent that
// ends a turn and is woken again (an orchestrator waiting on its children, a
// SendMessage) fires SubagentStart and SubagentStop once per turn (live: an
// orchestrator with 86 tool uses stored as 10, started after it stopped).
// started stays the first start, stopped and the totals follow the last stop.
func TestCallmeterResumedAgentKeepsItsFirstStartAndLatestTotals(t *testing.T) {
	lab := newCallmeterLab(t)
	scripted := lab.payloads("scripted.jsonl")
	agentTranscript, _ := payloadField(t, scripted[8], "agent_transcript_path").(string)
	first := lab.clock.Now().UnixMilli()
	lab.feed(scripted[5], asyncAgentResult(t, scripted[9]), scripted[8])
	full, err := os.ReadFile(agentTranscript)
	if err != nil {
		t.Fatalf("read agent transcript: %v", err)
	}
	turn := `{"type":"user","uuid":"s-u2","timestamp":"2026-09-23T01:40:00.000Z","message":{"role":"user","content":"count again"}}` + "\n" +
		`{"type":"assistant","uuid":"s-a5","timestamp":"2026-09-23T01:40:01.000Z","message":{"id":"msg_demo_S4","model":"claude-sonnet-4-5",` +
		`"content":[{"type":"tool_use","id":"toolu_demo_again","name":"Bash","input":{"command":"wc -l fixture.go"}}],"stop_reason":"tool_use",` +
		`"usage":{"input_tokens":3,"cache_read_input_tokens":7500,"cache_creation_input_tokens":40,"output_tokens":30}}}` + "\n" +
		`{"type":"assistant","uuid":"s-a6","timestamp":"2026-09-23T01:40:02.000Z","message":{"id":"msg_demo_S5","model":"claude-sonnet-4-5",` +
		`"content":[{"type":"text","text":"151"}],"stop_reason":"end_turn",` +
		`"usage":{"input_tokens":2,"cache_read_input_tokens":7540,"cache_creation_input_tokens":60,"output_tokens":8}}}` + "\n"
	lab.write(agentTranscript, append(full, turn...))
	lab.clock.Advance(10 * time.Minute)
	lab.feed(scripted[5], scripted[8])
	expect(t, "resumed agent", lab.row("SELECT * FROM agents WHERE agent_id = ?", cmSubagent), map[string]any{
		"started":      first,
		"stopped":      lab.clock.Now().UnixMilli(),
		"total_tokens": 18107 + (3 + 7500 + 40 + 30) + (2 + 7540 + 60 + 8),
		"tool_uses":    3,
	})
}

// A PostToolBatch carries no top-level tool_use_id: a store it cannot open
// still names every call whose record is lost.
func TestCallmeterStoreUnopenableNamesBatchCalls(t *testing.T) {
	lab := newCallmeterLab(t)
	blocker := filepath.Join(lab.root, "blocker")
	lab.write(blocker, []byte("a file where the store's directory should be"))
	var stderr bytes.Buffer
	batch := lab.payloads("probe5.jsonl")[3]
	storePath := filepath.Join(blocker, "callmeter.db")
	if code := runCallmeter(lab.ctx, strings.NewReader(batch), &stderr, storePath, lab.clock); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, id := range []string{cmBatchLead, "toolu_014w33S7y4Hmv2iQj3NzzEWV", "toolu_01LV57SCFxiU1LaMWZm3ixg6"} {
		if !strings.Contains(stderr.String(), id) {
			t.Errorf("stderr = %q, want the lost call %s named", stderr.String(), id)
		}
	}
	assertLogged(t, lab.rec, callmeter.StageStore)
}

// TestCallmeterBashCwdIsTheDirectoryBeforeTheCommand: PostToolUse's cwd
// follows the command's own `cd` (captured: PreToolUse /x, PostToolUse /x/pfm
// for `cd /x/pfm && …`), and the parser replays that `cd` from the stored
// cwd. The pre-command directory comes from PreToolUse; either hook may land
// first, and a call whose PreToolUse was never recorded keeps PostToolUse's.
func TestCallmeterBashCwdIsTheDirectoryBeforeTheCommand(t *testing.T) {
	payload := func(event, id, cwd string) string {
		m := map[string]any{
			"session_id": cmSessionA, "cwd": cwd, "hook_event_name": event,
			"transcript_path": cmDemoHome + "/.claude/projects/-tmp-demo-proj/" + cmSessionA + ".jsonl",
			"tool_name":       "Bash", "tool_use_id": id,
			"tool_input": map[string]any{"command": "cd sub && cat a.txt", "description": "read a"},
		}
		if event == "PostToolUse" {
			m["tool_response"] = map[string]any{"stdout": "line", "stderr": "", "interrupted": false}
			m["duration_ms"] = 5
		}
		encoded, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("encode %s payload: %v", event, err)
		}
		return string(encoded)
	}
	cases := []struct {
		name  string
		order []string
		want  string
	}{
		{"PreToolUse lands first", []string{"PreToolUse", "PostToolUse"}, cmDemoProj},
		{"PostToolUse lands first", []string{"PostToolUse", "PreToolUse"}, cmDemoProj},
		{"no PreToolUse recorded", []string{"PostToolUse"}, cmDemoProj + "/sub"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lab := newCallmeterLab(t)
			id := fmt.Sprintf("toolu_cwd%d", i)
			for _, event := range tc.order {
				cwd := cmDemoProj
				if event == "PostToolUse" {
					cwd += "/sub"
				}
				lab.feed(payload(event, id, cwd))
			}
			expect(t, "call", lab.call(id), map[string]any{"cwd": tc.want, "tool": "Bash", "duration_ms": 5})
		})
	}
}

// TestCallmeterUntypedAgentWithoutTranscriptIsNotRecorded: live, Claude Code
// fires SubagentStop for its own internal agents with no agent_type, no
// SubagentStart and no transcript on disk (16 in the first minutes of a busy
// chat). Such a stop is no sub-agent to meter: no row, no fault. A typed agent
// whose transcript is missing stays a fault.
func TestCallmeterUntypedAgentWithoutTranscriptIsNotRecorded(t *testing.T) {
	stop := func(agentID, agentType string) string {
		m := map[string]any{
			"session_id": cmSessionA, "hook_event_name": "SubagentStop", "agent_id": agentID,
			"transcript_path":       cmDemoHome + "/.claude/projects/-tmp-demo-proj/" + cmSessionA + ".jsonl",
			"agent_transcript_path": "/nonexistent/subagents/agent-" + agentID + ".jsonl",
		}
		if agentType != "" {
			m["agent_type"] = agentType
		}
		encoded, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("encode SubagentStop: %v", err)
		}
		return string(encoded)
	}
	lab := newCallmeterLab(t)
	lab.feed(stop("ainternal0000001", ""), stop("atyped0000000001", "general-purpose"))
	if n := lab.count("SELECT count(*) FROM agents WHERE agent_id = 'ainternal0000001'"); n != 0 {
		t.Errorf("untyped transcript-less agent rows = %d, want 0", n)
	}
	if n := lab.count("SELECT count(*) FROM faults WHERE error LIKE '%ainternal0000001%'"); n != 0 {
		t.Errorf("faults naming the untyped agent = %d, want 0", n)
	}
	if n := lab.count("SELECT count(*) FROM faults WHERE stage = ? AND error LIKE '%atyped0000000001%'",
		callmeter.StageTranscript); n != 1 {
		t.Errorf("transcript faults naming the typed agent = %d, want 1", n)
	}
}

// TestCallmeterFailedBashCountsItsOutput: a failed Bash call's output reaches
// the model as the PostToolUseFailure `error` text (live: "Exit code 1\n…",
// thousands of bytes), so that text is the call's real size; a failing suite
// is exactly the costly output the commands report exists to find.
func TestCallmeterFailedBashCountsItsOutput(t *testing.T) {
	lab := newCallmeterLab(t)
	output := "Exit code 1\n" + strings.Repeat("FAIL some_test\n", 40)
	encoded, err := json.Marshal(map[string]any{
		"session_id": cmSessionA, "hook_event_name": "PostToolUseFailure", "cwd": cmDemoProj,
		"transcript_path": cmDemoHome + "/.claude/projects/-tmp-demo-proj/" + cmSessionA + ".jsonl",
		"tool_name":       "Bash", "tool_use_id": "toolu_failed01",
		"tool_input": map[string]any{"command": "go test ./..."}, "error": output,
	})
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	lab.feed(string(encoded))
	expect(t, "failed call", lab.call("toolu_failed01"), map[string]any{"failed": 1, "bytes_real": len(output)})
}

// TestCallmeterBatchResolvesEarlierPending: a batch whose request is not on
// disk yet goes pending, and used to wait for the chat's Stop — for a
// sub-agent running an hour, its context sizes stayed unknown the whole hour
// (live: 11 pending, every id already on disk). The next batch of the same
// chat resolves it.
func TestCallmeterBatchResolvesEarlierPending(t *testing.T) {
	lab := newCallmeterLab(t)
	transcript := lab.transcript(cmSessionA)
	batch := func(id string) string {
		encoded, err := json.Marshal(map[string]any{
			"session_id": cmSessionA, "hook_event_name": "PostToolBatch", "transcript_path": transcript,
			"tool_calls": []map[string]any{{"tool_use_id": id, "tool_response": "ok"}},
		})
		if err != nil {
			t.Fatalf("encode batch: %v", err)
		}
		return string(encoded)
	}
	entry := func(msg, id string) string {
		return `{"type":"assistant","timestamp":"2026-09-23T01:00:00.000Z","message":{"id":"` + msg +
			`","content":[{"type":"tool_use","id":"` + id + `"}],"usage":{"input_tokens":5,` +
			`"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":1}}}` + "\n"
	}
	lab.write(transcript, []byte(""))
	lab.feed(batch("toolu_early"))
	lab.write(transcript, []byte(entry("msg_early", "toolu_early")+entry("msg_late", "toolu_late")))
	lab.feed(batch("toolu_late"))
	if n := lab.count("SELECT count(*) FROM requests WHERE pending = 1"); n != 0 {
		t.Errorf("pending requests after the next batch = %d, want 0", n)
	}
	expect(t, "early call", lab.call("toolu_early"), map[string]any{"request_id": "msg_early"})
}

// TestCallmeterBatchUntypedAgentWithoutTranscriptIsNotRecorded:
// PostToolBatch used to call callmeter.FindRequests against the missing
// transcript of one of Claude Code's own internal agents (agent_id set,
// agent_type empty, never a SubagentStart), write a fault, then write a
// pending request and call resolvePending against the same missing file for
// a second fault — 2 faults, 1 call and 1 pending request for an agent
// nothing meters. recordAgent already exempts this class (os.Stat,
// fs.ErrNotExist); recordBatch now shares the same check.
func TestCallmeterBatchUntypedAgentWithoutTranscriptIsNotRecorded(t *testing.T) {
	lab := newCallmeterLab(t)
	batch := func(agentID, agentType, id string) string {
		m := map[string]any{
			"session_id": cmSessionA, "hook_event_name": "PostToolBatch",
			"transcript_path": lab.transcript(cmSessionA), "agent_id": agentID,
			"tool_calls": []map[string]any{{"tool_use_id": id, "tool_response": "ok"}},
		}
		if agentType != "" {
			m["agent_type"] = agentType
		}
		encoded, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("encode batch: %v", err)
		}
		return string(encoded)
	}
	lab.feed(batch("ainternal0000001", "", "toolu_untyped001"))
	if n := lab.count("SELECT count(*) FROM faults"); n != 0 {
		t.Errorf("faults for the untyped agent's batch = %d, want 0", n)
	}
	if n := lab.count("SELECT count(*) FROM calls WHERE tool_use_id = 'toolu_untyped001'"); n != 0 {
		t.Errorf("calls for the untyped agent's batch = %d, want 0", n)
	}
	if n := lab.count("SELECT count(*) FROM requests"); n != 0 {
		t.Errorf("requests for the untyped agent's batch = %d, want 0", n)
	}
	// A typed agent's missing transcript still names exactly one transcript
	// fault: FindRequests fails once, and resolvePending is skipped rather
	// than failing the same read again.
	lab.feed(batch("atyped0000000001", "general-purpose", "toolu_typed001"))
	if n := lab.count("SELECT count(*) FROM faults WHERE stage = ? AND error LIKE '%atyped0000000001%'",
		callmeter.StageTranscript); n != 1 {
		t.Errorf("transcript faults naming the typed agent = %d, want 1", n)
	}
}
