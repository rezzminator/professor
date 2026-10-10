package costsink

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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

func TestSinkAnswers400ForABrokenBodyAndLogsWhy(t *testing.T) {
	sink, _, _, logs := newTestSink(t)
	if code := post(t, sink, http.MethodPost, "/v1/logs", []byte("{broken"), nil).Code; code != http.StatusBadRequest {
		t.Fatalf("broken POST = %d, want 400", code)
	}
	if !strings.Contains(logs.String(), "/v1/logs") {
		t.Fatalf("sink log = %q, want a line naming the failed path", logs.String())
	}
}

// Any local process can POST to the sink: a session id outside the ledger's character class never
// reaches a path join, nothing is written, and the refusal is logged.
func TestSinkNeverWritesOutsideTheLedgerDirForAnUnsafeSessionID(t *testing.T) {
	sink, _, dir, logs := newTestSink(t)
	parent := filepath.Dir(dir)
	for _, session := range []string{"../escape", "a/b", "", "x y"} {
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
