package report

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

// sizedRequest writes one request row; tokens < 0 writes it pending, unsized.
func sizedRequest(t *testing.T, store *callmeter.Store, id, session, agent string, ts, tokens int64) {
	t.Helper()
	r := callmeter.Request{RequestID: id, SessionID: &session, TS: &ts, Pending: callmeter.Ptr(tokens < 0)}
	if agent != "" {
		r.AgentID = &agent
	}
	if tokens >= 0 {
		r.ContextTokens = &tokens
	}
	if err := store.UpsertRequest(context.Background(), r, callmeter.Overwrite); err != nil {
		t.Fatalf("UpsertRequest %s: %v", id, err)
	}
}

// inRequest is a call of request req by agent (or the main chat when "").
func inRequest(c callmeter.Call, req, agent, agentType string) callmeter.Call {
	c.RequestID = &req
	c.AgentID = nil
	if agent != "" {
		c.AgentID = &agent
		c.AgentType = &agentType
	}
	return c
}

func TestContextStartPeakGrowthNamedByPredecessorCalls(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	dir := workDir(t)
	// Agent A: 1000 → 5000 → 5500 → 9000, then one request still pending.
	sizedRequest(t, store, "a1", "s", "A", ms(5*time.Hour), 1000)
	sizedRequest(t, store, "a2", "s", "A", ms(4*time.Hour), 5000)
	sizedRequest(t, store, "a3", "s", "A", ms(3*time.Hour), 5500)
	sizedRequest(t, store, "a4", "s", "A", ms(2*time.Hour), 9000)
	sizedRequest(t, store, "pending:a5c", "s", "A", ms(time.Hour), -1)
	readA := read("a1r", "A", dir+"/a.go", 5*time.Hour, 3000)
	readA.Cwd = &dir
	seed(t, store, inRequest(readA, "a1", "A", "general"))
	goTest := bash("a1b", "s", "A", ms(5*time.Hour), dir, "go test ./... | tail", 900)
	seed(t, store, inRequest(goTest, "a1", "A", "general"))
	grep := callmeter.Call{
		ToolUseID: "a2g", SessionID: callmeter.Ptr("s"), TS: callmeter.Ptr(ms(4 * time.Hour)),
		Tool: callmeter.Ptr("Grep"), Cwd: &dir, BytesDelivered: callmeter.Ptr(int64(400)),
	}
	seed(t, store, inRequest(grep, "a2", "A", "general"))
	readB := read("a3r", "A", dir+"/b.go", 3*time.Hour, 3400)
	readB.Cwd = &dir
	seed(t, store, inRequest(readB, "a3", "A", "general"))
	seed(t, store, inRequest(bash("a4b", "s", "A", ms(2*time.Hour), dir, "ls", 10), "a4", "A", "general"))
	seed(t, store, inRequest(bash("a5c", "s", "A", ms(time.Hour), dir, "ls", 10), "pending:a5c", "A", "general"))
	// The main chat: 2000 → 2500, below A's peak.
	sizedRequest(t, store, "m1", "s", "", ms(5*time.Hour), 2000)
	sizedRequest(t, store, "m2", "s", "", ms(4*time.Hour), 2500)
	seed(t, store, inRequest(read("m1r", "", dir+"/c.go", 5*time.Hour, 500), "m1", "", ""))
	seed(t, store, inRequest(read("m2r", "", dir+"/c.go", 4*time.Hour, 500), "m2", "", ""))
	if _, err := EnsureParsed(ctx, store, "", nil); err != nil {
		t.Fatalf("EnsureParsed: %v", err)
	}
	table, err := Context(ctx, store, Filter{}, func(string) (string, error) { return "chat-s", nil })
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	want := [][]string{
		{
			"chat-s", "s", "A", "general", "4", "1000", "9000", "2666",
			"+4000 Bash go test, Read a.go", "+3500 Read b.go", "+500 Grep",
		},
		{"chat-s", "s", "-", "-", "2", "2000", "2500", "500", "+500 Read c.go", "-", "-"},
	}
	if !reflect.DeepEqual(table.Rows, want) {
		t.Errorf("rows = %v\nwant   %v (header %v)", table.Rows, want, table.Header)
	}
	out := render(t, table)
	if !strings.Contains(out, "note: 1 requests still pending") {
		t.Errorf("report lacks the pending-request note:\n%s", out)
	}
}

func TestContextEmptyWindow(t *testing.T) {
	table, err := Context(context.Background(), openStore(t), Filter{}, nil)
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(render(t, table)), "\n")
	if len(lines) != 2 || lines[1] != EmptyLine {
		t.Errorf("empty report = %q, want heading plus %q", lines, EmptyLine)
	}
}
