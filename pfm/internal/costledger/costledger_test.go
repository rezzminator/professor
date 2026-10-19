package costledger

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const sessionA = "00000000-0000-4000-8000-0000000000a1"

func micros(v int64) *int64 { return &v }

func row(id, join string, cost *int64) Row {
	return Row{
		RequestID: id, TS: "2026-10-09T18:27:28.025Z", SessionID: sessionA, QuerySource: "repl_main_thread",
		Model: "claude-opus-5-5", InputTokens: 2, OutputTokens: 323, CacheReadTokens: 76447,
		CacheCreationTokens: 15924, CostUSDMicros: cost, Speed: "normal", Effort: "medium", Join: join,
	}
}

func TestAppendWritesTheContractLineInKeyOrderWithPrivateModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	if err := Append(dir, row("req_1", JoinLogSpan, micros(149149))); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, Row{RequestID: "req_2", SessionID: sessionA, Join: JoinSpan}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, sessionA+".jsonl")
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"request_id":"req_1","ts":"2026-10-09T18:27:28.025Z","session_id":"` + sessionA + `","agent_id":"","parent_agent_id":"",` +
		`"query_source":"repl_main_thread","model":"claude-opus-5-5","input_tokens":2,"output_tokens":323,"cache_read_tokens":76447,` +
		`"cache_creation_tokens":15924,"cost_usd_micros":149149,"speed":"normal","effort":"medium","join":"log+span"}` + "\n" +
		`{"request_id":"req_2","ts":"","session_id":"` + sessionA + `","agent_id":"","parent_agent_id":"","query_source":"","model":"",` +
		`"input_tokens":0,"output_tokens":0,"cache_read_tokens":0,"cache_creation_tokens":0,"cost_usd_micros":null,"speed":"","effort":"","join":"span"}` + "\n"
	if string(got) != want {
		t.Fatalf("ledger bytes\n got %s\nwant %s", got, want)
	}
	for path, mode := range map[string]os.FileMode{dir: 0o700, file: 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s mode = %v, %v; want %v", path, info.Mode().Perm(), err, mode)
		}
	}
}

func TestSessionIDMustBeSafeBeforeAnyPathJoin(t *testing.T) {
	bad := []string{"", ".", "..", "../escape", "a/b", `a\b`, "a b", "a\x00b", "é", strings.Repeat("a", 129), "a\nb"}
	for _, id := range bad {
		if ValidSessionID(id) {
			t.Errorf("ValidSessionID(%q) = true", id)
		}
		if path, err := Path(t.TempDir(), id); err == nil {
			t.Errorf("Path(%q) = %q, want an error", id, path)
		}
		if _, err := ForSession(t.TempDir(), id); err == nil {
			t.Errorf("ForSession(%q) must not return another scope's data", id)
		}
		if err := Append(t.TempDir(), Row{RequestID: "r", SessionID: id}); id != "" && err == nil {
			t.Errorf("Append with session %q succeeded", id)
		}
	}
	for _, id := range []string{sessionA, "a", "A.b_c-d", strings.Repeat("a", 128)} {
		if !ValidSessionID(id) {
			t.Errorf("ValidSessionID(%q) = false", id)
		}
	}
	if err := Append(t.TempDir(), Row{SessionID: sessionA}); err == nil {
		t.Error("Append without a request_id succeeded")
	}
}

func TestUnattributedKeepsObservationsAndDeduplicatesOnlyRealIdentity(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"", "", "req_bg", "req_bg"} {
		r := row(id, JoinLog, micros(50))
		r.SessionID = ""
		if err := Append(dir, r); err != nil {
			t.Fatal(err)
		}
	}
	ledger, err := Unattributed(dir)
	if err != nil || ledger.Faults != 0 || len(ledger.Rows) != 3 || Sum(ledger.Rows).Micros != 150 {
		t.Fatalf("unattributed ledger = %+v, %v; want two observations and one deduped request", ledger, err)
	}
	for _, ids := range [][2]string{{"", ""}, {strings.Repeat("0", 32), strings.Repeat("0", 16)}, {"bad", "bad"}} {
		if got := (Row{TraceID: ids[0], SpanID: ids[1]}).Identity(); got != "" {
			t.Errorf("invalid trace identity %v = %q", ids, got)
		}
	}
}

func TestSessionObservationsStayOutsideLegacyLedgers(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, row("", JoinLog, micros(25))); err != nil {
		t.Fatal(err)
	}
	ids, err := SessionIDs(dir)
	if err != nil || len(ids) != 1 || ids[0] != sessionA {
		t.Fatalf("observation-only sessions = %v, %v", ids, err)
	}
	legacy, err := Path(dir, sessionA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("request-less observation reached legacy reader: %v", err)
	}
	if err := Append(dir, row("req_real", JoinLog, micros(50))); err != nil {
		t.Fatal(err)
	}
	ledger, err := ForSession(dir, sessionA)
	if err != nil || !ledger.Present || ledger.Faults != 0 || len(ledger.Rows) != 2 || Sum(ledger.Rows).Micros != 75 {
		t.Fatalf("combined ledger = %+v, %v", ledger, err)
	}
	ids, err = SessionIDs(dir)
	if err != nil || len(ids) != 1 {
		t.Fatalf("duplicate session discovery = %v, %v", ids, err)
	}
	observation, err := Path(ObservationsDir(dir), sessionA)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-100 * 24 * time.Hour)
	if err := os.Chtimes(observation, old, old); err != nil {
		t.Fatal(err)
	}
	if removed, err := Prune(dir, time.Now(), 90*24*time.Hour); err != nil || removed != 1 {
		t.Fatalf("prune observations = %d, %v", removed, err)
	}
	if err := os.WriteFile(ObservationsDir(dir)+"-file", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ObservationsDir(dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(ObservationsDir(dir)+"-file", ObservationsDir(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := SessionIDs(dir); err == nil {
		t.Fatal("unreadable observations rendered as absent")
	}
}

func TestConcurrentAppendsNeverInterleaveALine(t *testing.T) {
	dir := t.TempDir()
	const writers, per = 8, 40
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				r := row(
					"req_"+strings.Repeat("x", 3000)+string(rune('a'+w))+"_"+string(rune('a'+i%26)),
					JoinLog,
					micros(1),
				)
				if err := Append(dir, r); err != nil {
					t.Error(err)
				}
			}
		}(w)
	}
	wg.Wait()
	data, err := os.ReadFile(filepath.Join(dir, sessionA+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	if len(lines) != writers*per {
		t.Fatalf("%d lines, want %d", len(lines), writers*per)
	}
	for n, line := range lines {
		var r Row
		if err := json.Unmarshal(line, &r); err != nil {
			t.Fatalf("line %d is not one intact JSON object: %v", n, err)
		}
	}
}

func TestReadDedupesByBestJoinThenLaterLine(t *testing.T) {
	dir := t.TempDir()
	rows := []Row{
		row("req_a", JoinSpan, nil),
		row("req_b", JoinLog, micros(10)),
		row("req_a", JoinLogSpan, micros(30)),
		row("req_b", JoinLog, micros(11)),
		row("req_c", JoinLogSpan, micros(50)),
		row("req_c", JoinLog, micros(99)),
	}
	for _, r := range rows {
		if err := Append(dir, r); err != nil {
			t.Fatal(err)
		}
	}
	ledger, err := ForSession(dir, sessionA)
	if err != nil || !ledger.Present || ledger.Faults != 0 {
		t.Fatalf("Read = %+v, %v", ledger, err)
	}
	type kept struct {
		id, join string
		cost     int64
	}
	var got []kept
	for _, r := range ledger.Rows {
		got = append(got, kept{r.RequestID, r.Join, *r.CostUSDMicros})
	}
	want := []kept{{"req_a", JoinLogSpan, 30}, {"req_b", JoinLog, 11}, {"req_c", JoinLogSpan, 50}}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestReadCountsAFaultForATornTailAndIgnoresUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, sessionA+".jsonl")
	good, _ := json.Marshal(row("req_1", JoinLog, micros(7)))
	body := string(good) + "\n" +
		`{"request_id":"req_2","session_id":"` + sessionA + `","cost_usd_micros":5,"join":"log","future_key":{"x":1}}` + "\n" +
		"not json at all\n" +
		`{"request_id":"req_3","ts":"2026`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ledger, err := ForSession(dir, sessionA)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Rows) != 2 || ledger.Faults != 2 {
		t.Fatalf("rows=%d faults=%d, want 2 rows and 2 faults", len(ledger.Rows), ledger.Faults)
	}
}

func TestReadAMissingLedgerIsNotPresentAndNotAnError(t *testing.T) {
	for _, dir := range []string{t.TempDir(), filepath.Join(t.TempDir(), "never-created")} {
		ledger, err := ForSession(dir, sessionA)
		if err != nil || ledger.Present || len(ledger.Rows) != 0 {
			t.Fatalf("ForSession(%s) = %+v, %v; want not present, no error", dir, ledger, err)
		}
	}
	if _, err := ForSession(t.TempDir(), "../x"); err == nil {
		t.Fatal("Read with an unsafe session id succeeded")
	}
}

// The contract sample is 15 invented rows totalling 1294100 micros.
func TestSampleLedgerTotalsAndSplitsSideCalls(t *testing.T) {
	dir := t.TempDir()
	sample, err := os.ReadFile(filepath.Join("testdata", "sample-15.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionA+".jsonl"), sample, 0o600); err != nil {
		t.Fatal(err)
	}
	ledger, err := ForSession(dir, sessionA)
	if err != nil || len(ledger.Rows) != 15 || ledger.Faults != 0 {
		t.Fatalf("Read = %d rows, %d faults, %v", len(ledger.Rows), ledger.Faults, err)
	}
	total := Sum(ledger.Rows)
	if total.Micros != 1294100 || total.UnknownCost != 0 || total.Rows != 15 {
		t.Fatalf("Sum = %+v, want 1294100 micros over 15 rows", total)
	}
	var in, out int64
	for _, r := range ledger.Rows {
		in += r.InputTokens
		out += r.OutputTokens
	}
	if total.InputTokens != in || total.OutputTokens != out || in == 0 {
		t.Fatalf("token sums = %d/%d, want %d/%d", total.InputTokens, total.OutputTokens, in, out)
	}
	held := map[string]bool{}
	for _, r := range ledger.Rows[:10] {
		held[r.RequestID] = true
	}
	side := ledger.Side(func(id string) bool { return held[id] })
	if len(side) != 5 || side[0].RequestID != ledger.Rows[10].RequestID {
		t.Fatalf("Side = %d rows, want the last 5 in order", len(side))
	}
}

func TestSumNamesUnknownCostNeverAsZero(t *testing.T) {
	total := Sum([]Row{row("a", JoinLogSpan, micros(100)), row("b", JoinSpan, nil), row("c", JoinLog, micros(20))})
	if total.Micros != 120 || total.UnknownCost != 1 || total.Rows != 3 {
		t.Fatalf("Sum = %+v, want 120 micros, 1 unknown cost, 3 rows", total)
	}
}

func TestPruneRemovesOnlyOldLedgerFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	files := map[string]time.Duration{
		"old.jsonl": 91 * 24 * time.Hour, "edge.jsonl": 89 * 24 * time.Hour, "fresh.jsonl": time.Hour,
		"keep.txt": 400 * 24 * time.Hour,
	}
	for name, age := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := Prune(dir, now, 90*24*time.Hour)
	if err != nil || removed != 1 {
		t.Fatalf("Prune = %d, %v; want 1", removed, err)
	}
	for name, wantKept := range map[string]bool{"old.jsonl": false, "edge.jsonl": true, "fresh.jsonl": true, "keep.txt": true} {
		_, err := os.Stat(filepath.Join(dir, name))
		if (err == nil) != wantKept {
			t.Errorf("%s kept = %v, want %v", name, err == nil, wantKept)
		}
	}
	if n, err := Prune(filepath.Join(dir, "missing"), now, time.Hour); err != nil || n != 0 {
		t.Fatalf("Prune of a missing dir = %d, %v; want 0, nil", n, err)
	}
}
