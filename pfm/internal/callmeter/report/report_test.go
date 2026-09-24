package report

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

var testNow = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

func openStore(t *testing.T) *callmeter.Store {
	t.Helper()
	store, err := callmeter.OpenDB(context.Background(), filepath.Join(t.TempDir(), "callmeter.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

// seed writes one call through the store API.
func seed(t *testing.T, store *callmeter.Store, c callmeter.Call) {
	t.Helper()
	if err := store.UpsertCall(context.Background(), c, callmeter.Overwrite); err != nil {
		t.Fatalf("UpsertCall %s: %v", c.ToolUseID, err)
	}
}

// bash is a Bash call of session s, agent a, ts ms, with command in cwd.
func bash(id, s, a string, ts int64, cwd, command string, delivered int64) callmeter.Call {
	input, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		panic(err)
	}
	return callmeter.Call{
		ToolUseID: id, SessionID: &s, AgentID: &a, TS: &ts, Tool: callmeter.Ptr("Bash"),
		Input: callmeter.Ptr(string(input)), Cwd: &cwd, BytesDelivered: &delivered,
	}
}

func ms(d time.Duration) int64 { return testNow.Add(-d).UnixMilli() }

func render(t *testing.T, table *Table) string {
	t.Helper()
	var out bytes.Buffer
	if err := table.Render(&out); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out.String()
}

func TestParseSince(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{"", testNow.Add(-Retention), false},
		{"7d", testNow.Add(-7 * 24 * time.Hour), false},
		{"24h", testNow.Add(-24 * time.Hour), false},
		{"2026-09-01", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), false},
		{"7w", time.Time{}, true},
		{"yesterday", time.Time{}, true},
	}
	for _, c := range cases {
		got, err := ParseSince(c.in, testNow)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseSince(%q) error = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && !got.Equal(c.want) {
			t.Errorf("ParseSince(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestPruneExpiredPrunesOldRowsBeforeReport(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	for id, age := range map[string]time.Duration{"old": 40 * 24 * time.Hour, "new": time.Hour} {
		seed(t, store, callmeter.Call{
			ToolUseID: id, SessionID: callmeter.Ptr("s"), TS: callmeter.Ptr(ms(age)), Tool: callmeter.Ptr("Read"),
			FilePath: callmeter.Ptr("/src/" + id + ".go"), BytesDelivered: callmeter.Ptr(int64(10)),
		})
	}
	removed, err := PruneExpired(ctx, store, testNow)
	if err != nil {
		t.Fatalf("PruneExpired: %v", err)
	}
	if removed != 1 {
		t.Errorf("PruneExpired removed %d rows, want 1", removed)
	}
	table, err := Files(ctx, store, Filter{}, nil)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(table.Rows) != 1 || table.Rows[0][0] != "/src/new.go" {
		t.Errorf("rows after prune = %v, want only /src/new.go", table.Rows)
	}
}

func TestEmptyWindowPrintsOneLine(t *testing.T) {
	table, err := Files(context.Background(), openStore(t), Filter{}, nil)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(render(t, table)), "\n")
	if len(lines) != 2 || lines[1] != EmptyLine {
		t.Errorf("empty report = %q, want heading plus %q", lines, EmptyLine)
	}
}

func TestProjectFilterKeepsCwdAndBelow(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	for id, cwd := range map[string]string{"in": "/w/p", "below": "/w/p/sub", "sibling": "/w/px"} {
		seed(t, store, callmeter.Call{
			ToolUseID: id, TS: callmeter.Ptr(ms(time.Hour)), Tool: callmeter.Ptr("Read"), Cwd: callmeter.Ptr(cwd),
			FilePath: callmeter.Ptr("/f/" + id), BytesDelivered: callmeter.Ptr(int64(1)),
		})
	}
	table, err := Files(ctx, store, Filter{Project: "/w/p"}, nil)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(table.Rows) != 2 {
		t.Errorf("project rows = %v, want /f/in and /f/below", table.Rows)
	}
}

func TestNameOfErrorRendersQuestionMarkAndNote(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	if err := store.AddFault(ctx, callmeter.Fault{
		TS: ms(time.Hour), SessionID: "s1", ToolUseID: "t1", Stage: callmeter.StageStore, Error: "disk full",
	}); err != nil {
		t.Fatalf("AddFault: %v", err)
	}
	failing := func(string) (string, error) { return "", errFleet }
	table, err := Faults(ctx, store, Filter{}, failing)
	if err != nil {
		t.Fatalf("Faults: %v", err)
	}
	var faultRow []string
	for _, row := range table.Rows {
		if row[0] == "fault" {
			faultRow = row
		}
	}
	if len(faultRow) < 5 || faultRow[4] != "?" {
		t.Errorf("fault row = %v, want chat column ?", faultRow)
	}
	out := render(t, table)
	if !strings.Contains(out, "note: chat names could not be read: session s1: fleet.db unreadable") {
		t.Errorf("report lacks the chat-name note:\n%s", out)
	}
	if !strings.Contains(out, "note: 1 calls not recorded") {
		t.Errorf("report lacks the unrecorded-calls note:\n%s", out)
	}
}

type fleetError string

func (e fleetError) Error() string { return string(e) }

const errFleet = fleetError("fleet.db unreadable")

// TestAccountFilterKeepsThatAccountOnly: Filter.Account keeps the calls and
// requests that account ran, drops another account's and the NULL account's,
// and the title names it.
func TestAccountFilterKeepsThatAccountOnly(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	accounts := map[string]*int64{"three": callmeter.Ptr(int64(3)), "one": callmeter.Ptr(int64(1)), "none": nil}
	for id, account := range accounts {
		seed(t, store, callmeter.Call{
			ToolUseID: id, TS: callmeter.Ptr(ms(time.Hour)), Tool: callmeter.Ptr("Read"), Cwd: callmeter.Ptr("/w"),
			FilePath: callmeter.Ptr("/f/" + id), BytesDelivered: callmeter.Ptr(int64(1)), Account: account,
		})
		if err := store.UpsertRequest(ctx, callmeter.Request{
			RequestID: "msg_" + id, TS: callmeter.Ptr(ms(time.Hour)), Account: account,
		}, callmeter.Overwrite); err != nil {
			t.Fatalf("UpsertRequest: %v", err)
		}
	}
	filter := Filter{Account: callmeter.Ptr(3)}
	table, err := Files(ctx, store, filter, nil)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if out := render(
		t,
		table,
	); len(table.Rows) != 1 || !strings.Contains(out, "/f/three") ||
		!strings.Contains(out, "account=3") {
		t.Errorf("account 3 files = %v, want only /f/three under an account=3 title\n%s", table.Rows, out)
	}
	where, args := requestFilter(filter)
	var requests string
	if err := store.DB().QueryRowContext(ctx,
		"SELECT group_concat(request_id) FROM requests r WHERE "+where, args...).Scan(&requests); err != nil {
		t.Fatalf("filtered requests: %v", err)
	}
	if requests != "msg_three" {
		t.Errorf("account 3 requests = %q, want msg_three", requests)
	}
}
