package costsink

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/costledger"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type steppedClock struct{ now time.Time }

func (c *steppedClock) Now() time.Time { return c.now }

func newTestSink(t *testing.T) (*Sink, *steppedClock, string, *syncBuffer) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ledger")
	logs := &syncBuffer{}
	clk := &steppedClock{now: epoch}
	sink := NewSink(SinkOptions{Dir: dir, Log: logs, Now: clk.Now, Wait: 20 * time.Second, MaxBody: 1 << 20})
	return sink, clk, dir, logs
}

func TestSinkAnswersOnlyOTLPJSONPostsOnLogsAndTraces(t *testing.T) {
	sink, _, _, _ := newTestSink(t)
	body := logBody(apiRequest(testSession, "req_1", "repl_main_thread", 100))
	for _, path := range []string{"/v1/logs", "/v1/traces"} {
		recorder := post(
			t,
			sink,
			http.MethodPost,
			path,
			map[string][]byte{"/v1/logs": body, "/v1/traces": spanBody()}[path],
			nil,
		)
		if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != "{}" {
			t.Errorf("POST %s = %d %q, want 200 {}", path, recorder.Code, recorder.Body.String())
		}
	}
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/v1/metrics", http.StatusNotFound},
		{http.MethodPost, "/", http.StatusNotFound},
		{http.MethodPost, "/v1/logs/extra", http.StatusNotFound},
		{http.MethodGet, "/v1/logs", http.StatusMethodNotAllowed},
		{http.MethodPut, "/v1/traces", http.StatusMethodNotAllowed},
	} {
		if got := post(t, sink, tc.method, tc.path, body, nil).Code; got != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestSinkAcceptsGzipAndCapsTheBody(t *testing.T) {
	sink, _, dir, _ := newTestSink(t)
	body := logBody(
		apiRequest(testSession, "req_1", "repl_main_thread", 100),
		apiRequest(testSession, "req_2", "repl_main_thread", 200),
	)
	if code := post(
		t,
		sink,
		http.MethodPost,
		"/v1/logs",
		gzipped(t, body),
		map[string]string{"Content-Encoding": "gzip"},
	).Code; code != http.StatusOK {
		t.Fatalf("gzip POST = %d", code)
	}
	sink.Drain()
	ledger, err := costledger.ForSession(dir, testSession)
	if err != nil || len(ledger.Rows) != 2 {
		t.Fatalf("ledger after gzip POST = %d rows, %v; want 2", len(ledger.Rows), err)
	}
	huge := bytes.Repeat([]byte("x"), (1<<20)+1)
	if code := post(t, sink, http.MethodPost, "/v1/logs", huge, nil).Code; code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized POST = %d, want 413", code)
	}
	bomb := gzipped(t, bytes.Repeat([]byte(" "), (1<<20)+1))
	if code := post(
		t,
		sink,
		http.MethodPost,
		"/v1/logs",
		bomb,
		map[string]string{"Content-Encoding": "gzip"},
	).Code; code != http.StatusRequestEntityTooLarge {
		t.Fatalf("gzip bomb POST = %d, want 413", code)
	}
}

func TestSinkKeepsUnattributedObservationsWithoutFabricatingIDsOrSpamming(t *testing.T) {
	sink, _, dir, logs := newTestSink(t)
	body := logBody(apiRequest("", "", "auto_mode", 125))
	for range 3 {
		response := post(t, sink, http.MethodPost, "/v1/logs", body, nil)
		if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != "{}" {
			t.Fatalf(
				"unattributed telemetry = %d %s; want accepted, not refused",
				response.Code,
				response.Body.String(),
			)
		}
	}
	sink.Drain()
	raw, err := os.ReadFile(filepath.Join(dir, "unattributed", "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	if len(lines) != 3 || sink.Faults() != 0 || strings.Count(logs.String(), "unattributed") != 1 {
		t.Fatalf(
			"observations=%d faults=%d log=%q; want all three stored and one warning",
			len(lines),
			sink.Faults(),
			logs.String(),
		)
	}
	for _, line := range lines {
		var row costledger.Row
		if err := json.Unmarshal(line, &row); err != nil || row.RequestID != "" || row.SessionID != "" ||
			row.QuerySource != "auto_mode" || row.CostUSDMicros == nil || *row.CostUSDMicros != 125 {
			t.Fatalf("unattributed row = %+v, %v; identity must remain absent", row, err)
		}
	}
}

func TestSinkDoesNotTreatLogTraceContextAsCallIdentity(t *testing.T) {
	for _, requestID := range []string{"", "req_from_log"} {
		t.Run(requestID, func(t *testing.T) {
			sink, clk, dir, _ := newTestSink(t)
			trace, spanID := "000000000000000000000000000000a1", "00000000000000a1"
			span := llmSpan(testSession, "", "", "", "auto_mode")
			span["traceId"], span["spanId"] = trace, spanID
			post(t, sink, http.MethodPost, "/v1/traces", spanBody(span), nil)
			post(t, sink, http.MethodPost, "/v1/traces", spanBody(span), nil) // A real span id dedupes.
			clk.now = clk.now.Add(time.Minute)
			sink.Flush() // A log's enclosing context is not proof it belongs to this llm span.
			body := mustJSON(map[string]any{"resourceLogs": []any{map[string]any{
				"scopeLogs": []any{map[string]any{"logRecords": []any{map[string]any{
					"traceId": trace, "spanId": spanID,
					"attributes": apiRequest(testSession, requestID, "auto_mode", 125),
				}}}},
			}}})
			post(t, sink, http.MethodPost, "/v1/logs", body, nil)
			post(t, sink, http.MethodPost, "/v1/logs", body, nil)
			for range 2 {
				post(t, sink, http.MethodPost, "/v1/logs",
					logBody(apiRequest(testSession, "", "prompt_suggestion", 50)), nil)
			}
			sink.Drain()
			ledger, err := costledger.ForSession(dir, testSession)
			wantRows, wantMicros := 4, int64(225)
			if requestID == "" {
				wantRows, wantMicros = 5, 350
			}
			spanRows := 0
			for _, row := range ledger.Rows {
				if row.Join == costledger.JoinSpan && row.RequestID == "" {
					spanRows++
				}
			}
			if err != nil || len(ledger.Rows) != wantRows || spanRows != 1 ||
				costledger.Sum(ledger.Rows).Micros != wantMicros ||
				costledger.Sum(ledger.Rows).UnknownCost != 1 {
				t.Fatalf("independent observations must remain separate = %+v, %v", ledger, err)
			}
		})
	}
}

func TestSinkAcceptsRequestScopedBackgroundWithoutSession(t *testing.T) {
	sink, _, dir, _ := newTestSink(t)
	post(t, sink, http.MethodPost, "/v1/logs", logBody(apiRequest("", "req_bg", "hook_prompt", 75)), nil)
	post(t, sink, http.MethodPost, "/v1/traces", spanBody(llmSpan("", "req_bg", "", "", "hook_prompt")), nil)
	sink.Drain()
	ledger, err := costledger.Unattributed(dir)
	if err != nil || len(ledger.Rows) != 1 || ledger.Rows[0].SessionID != "" ||
		ledger.Rows[0].RequestID != "req_bg" || ledger.Rows[0].Join != costledger.JoinLogSpan {
		t.Fatalf("request-scoped background = %+v, %v", ledger, err)
	}
}

func TestSinkAnswers400ForABrokenBodyAndLogsWhy(t *testing.T) {
	sink, _, _, logs := newTestSink(t)
	if code := post(t, sink, http.MethodPost, "/v1/logs", []byte("{broken"), nil).Code; code != http.StatusBadRequest {
		t.Fatalf("broken POST = %d, want 400", code)
	}
	if !strings.Contains(logs.String(), "/v1/logs") {
		t.Fatalf("sink log = %q, want a line naming the failed path", logs.String())
	}
}

// A record with an unsafe session is a fault, not a refused POST: the batch's other records are
// joined, the sink answers 200 with the OTLP partial success naming how many it refused, and each
// refused record counts once.
func TestSinkAnswers200ForAPartiallyRefusedBatchAndCountsTheRefusal(t *testing.T) {
	cases := []struct {
		name, path, rejectedField string
		body                      []byte
		faults                    int
	}{
		{"logs", "/v1/logs", "rejectedLogRecords", logBody(
			apiRequest(testSession, "req_good", "repl_main_thread", 100),
			apiRequest("../bad", "req_bad", "repl_main_thread", 5),
			apiRequest("bad/path", "req_unsafe", "repl_main_thread", 5),
		), 2},
		{"traces", "/v1/traces", "rejectedSpans", spanBody(
			llmSpan(testSession, "req_good", "", "", "repl_main_thread"),
			llmSpan("../bad", "req_bad", "", "", "repl_main_thread"),
		), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sink, _, _, logs := newTestSink(t)
			response := post(t, sink, http.MethodPost, c.path, c.body, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("partially refused batch = %d, want 200", response.Code)
			}
			var answer struct {
				PartialSuccess map[string]string `json:"partialSuccess"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil ||
				answer.PartialSuccess[c.rejectedField] != strconv.Itoa(c.faults) ||
				!strings.Contains(answer.PartialSuccess["errorMessage"], "refused") {
				t.Fatalf("body = %q (%v); want partialSuccess.%s = %d with the reason",
					response.Body.String(), err, c.rejectedField, c.faults)
			}
			if sink.Faults() != c.faults || !strings.Contains(logs.String(), "refused") {
				t.Fatalf("faults = %d, log = %q; want %d refused records counted and logged",
					sink.Faults(), logs.String(), c.faults)
			}
			sink.Drain()
			ledger, err := costledger.ForSession(sink.dir, testSession)
			if err != nil || len(ledger.Rows) != 1 || ledger.Rows[0].RequestID != "req_good" {
				t.Fatalf("ledger = %v, %v; want the good record kept", ledger, err)
			}
		})
	}
}

// A log without its own stamp no longer takes the sink's clock: the joined row carries the span's
// end. The rest of the fallback order is the joiner's (joiner_test.go).
func TestSinkStampsAJoinedRowWithTheCallsOwnTimeNotTheReceiptTime(t *testing.T) {
	sink, _, dir, _ := newTestSink(t)
	logAttrs := withoutKey(apiRequest(testSession, "req_1", "repl_main_thread", 100), "event.timestamp")
	post(t, sink, http.MethodPost, "/v1/logs", logBody(logAttrs), nil)
	span := llmSpan(testSession, "req_1", "", "", "repl_main_thread")
	post(t, sink, http.MethodPost, "/v1/traces", spanBody(span), nil)
	sink.Drain()
	ledger, err := costledger.ForSession(dir, testSession)
	if err != nil || len(ledger.Rows) != 1 {
		t.Fatalf("ledger = %v, %v", ledger, err)
	}
	if got := ledger.Rows[0].TS; got != "2026-10-09T18:27:28.025Z" {
		t.Fatalf("joined row ts = %q, want the span's end", got)
	}
}

func withoutKey(attrs []map[string]any, key string) []map[string]any {
	var out []map[string]any
	for _, a := range attrs {
		if a["key"] != key {
			out = append(out, a)
		}
	}
	return out
}

// Any local process can POST to the sink: a session id outside the ledger's character class never
// reaches a path join, nothing is written, and the refusal is logged.
func TestSinkNeverWritesOutsideTheLedgerDirForAnUnsafeSessionID(t *testing.T) {
	sink, _, dir, logs := newTestSink(t)
	parent := filepath.Dir(dir)
	for _, session := range []string{"../escape", "a/b", "x y"} {
		body := logBody(apiRequest(session, "req_"+strings.ReplaceAll(session, "/", "_"), "repl_main_thread", 100))
		post(t, sink, http.MethodPost, "/v1/logs", body, nil)
	}
	sink.Drain()
	entries, _ := os.ReadDir(parent)
	for _, entry := range entries {
		if entry.Name() != "ledger" {
			t.Errorf("unexpected entry %q beside the ledger dir", entry.Name())
		}
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*")); len(files) != 0 {
		t.Errorf("ledger dir holds %v, want nothing", files)
	}
	if sink.Faults() == 0 || !strings.Contains(logs.String(), "session") {
		t.Errorf("faults = %d, log = %q; want the refusals counted and logged", sink.Faults(), logs.String())
	}
}

type post25 struct {
	T    float64         `json:"t"`
	Path string          `json:"path"`
	Body json.RawMessage `json:"body"`
}

func loadProbeSession(t *testing.T) []post25 {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", "probe-session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }() // read-only fixture
	var posts []post25
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 8<<20)
	for scanner.Scan() {
		var p post25
		if err := json.Unmarshal(scanner.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		posts = append(posts, p)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return posts
}

// replay feeds the probe session's POSTs, at their recorded arrival times, to a sink writing a
// real ledger, up to maxT seconds, and returns the deduped ledger after the final drain.
func replay(t *testing.T, maxT float64) (costledger.Ledger, string) {
	t.Helper()
	sink, clk, dir, _ := newTestSink(t)
	for _, p := range loadProbeSession(t) {
		if p.T > maxT {
			continue
		}
		clk.now = epoch.Add(time.Duration(p.T * float64(time.Second)))
		sink.Flush()
		if code := post(t, sink, http.MethodPost, p.Path, p.Body, nil).Code; code != http.StatusOK {
			t.Fatalf("replay POST %s at t=%v = %d", p.Path, p.T, code)
		}
	}
	sink.Drain()
	ledger, err := costledger.ForSession(dir, testSession)
	if err != nil {
		t.Fatal(err)
	}
	return ledger, filepath.Join(dir, testSession+".jsonl")
}

// The first 15 API calls of the probe session — the acceptance numbers — are 15 rows, every one
// joined from both halves, totalling 1294100 micros, split across the agents exactly as the
// reference join splits them.
func TestReplayOfTheProbeSessionJoinsFifteenRowsTo1294100(t *testing.T) {
	ledger, file := replay(t, 430)
	total := costledger.Sum(ledger.Rows)
	if len(ledger.Rows) != 15 || total.Micros != 1294100 || total.UnknownCost != 0 || ledger.Faults != 0 {
		t.Fatalf(
			"replay = %d rows, %d micros, %d unknown, %d faults; want 15 rows, 1294100 micros",
			len(ledger.Rows),
			total.Micros,
			total.UnknownCost,
			ledger.Faults,
		)
	}
	byAgent := map[string]int64{}
	for _, r := range ledger.Rows {
		if r.Join != costledger.JoinLogSpan {
			t.Errorf("row %s joined as %q, want log+span", r.RequestID, r.Join)
		}
		byAgent[r.AgentID] += *r.CostUSDMicros
	}
	want := map[string]int64{
		"":                  340251,
		"a0000000000000001": 78574,
		"a0000000000000002": 787435,
		"a0000000000000003": 87840,
	}
	for agent, micros := range want {
		if byAgent[agent] != micros {
			t.Errorf("agent %q = %d micros, want %d", agent, byAgent[agent], micros)
		}
	}
	if len(byAgent) != len(want) {
		t.Errorf("agents = %v, want only %v", byAgent, want)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if contains(
		string(raw),
		"example.invalid",
		"user_fixture",
		"organization",
		"00000000-0000-4000-8000-00000000b001",
		"terminal",
	) {
		t.Errorf("the ledger carries identity or noise fields: %.300s", raw)
	}
}

func TestReplayOfTheWholeProbeSessionIsTwentyOneRowsTo1435027(t *testing.T) {
	ledger, _ := replay(t, 1e9)
	total := costledger.Sum(ledger.Rows)
	if len(ledger.Rows) != 21 || total.Micros != 1435027 || total.UnknownCost != 0 {
		t.Fatalf(
			"replay = %d rows, %d micros, %d unknown; want 21 rows, 1435027 micros",
			len(ledger.Rows),
			total.Micros,
			total.UnknownCost,
		)
	}
	querySources := map[string]int{}
	for _, r := range ledger.Rows {
		querySources[r.QuerySource]++
	}
	for _, source := range []string{"web_search_tool", "web_fetch_apply", "agent_summary", "repl_main_thread", "agent:builtin:general-purpose"} {
		if querySources[source] == 0 {
			t.Errorf("no row with query_source %q in %v", source, querySources)
		}
	}
}
