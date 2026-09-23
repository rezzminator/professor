package backfill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

// orphanNow is the fixed instant every case below measures its 10-minute
// grace against.
var orphanNow = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

// seedPending upserts one pending request with one call carrying it, agentID
// and agentType as given (agentType "" leaves the column NULL), ts age before
// orphanNow.
func seedPending(t *testing.T, store *callmeter.Store, requestID, sessionID, agentID, agentType string, age time.Duration) {
	t.Helper()
	ctx := context.Background()
	ts := orphanNow.Add(-age).UnixMilli()
	call := callmeter.Call{
		ToolUseID: "toolu_" + requestID,
		SessionID: callmeter.Ptr(sessionID),
		RequestID: callmeter.Ptr(requestID),
		TS:        callmeter.Ptr(ts),
	}
	if agentID != "" {
		call.AgentID = callmeter.Ptr(agentID)
	}
	if agentType != "" {
		call.AgentType = callmeter.Ptr(agentType)
	}
	if err := store.UpsertCall(ctx, call, callmeter.Overwrite); err != nil {
		t.Fatalf("seed call: %v", err)
	}
	req := callmeter.Request{
		RequestID: requestID,
		SessionID: callmeter.Ptr(sessionID),
		TS:        callmeter.Ptr(ts),
		Pending:   callmeter.Ptr(true),
	}
	if agentID != "" {
		req.AgentID = callmeter.Ptr(agentID)
	}
	if err := store.UpsertRequest(ctx, req, callmeter.Overwrite); err != nil {
		t.Fatalf("seed request: %v", err)
	}
}

func requestExists(t *testing.T, store *callmeter.Store, requestID string) bool {
	t.Helper()
	var n int
	if err := store.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM requests WHERE request_id = ?", requestID).Scan(&n); err != nil {
		t.Fatalf("count requests: %v", err)
	}
	return n > 0
}

func callExists(t *testing.T, store *callmeter.Store, toolUseID string) bool {
	t.Helper()
	var n int
	if err := store.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM calls WHERE tool_use_id = ?", toolUseID).Scan(&n); err != nil {
		t.Fatalf("count calls: %v", err)
	}
	return n > 0
}

// TestRetireOrphansCases covers the five rules of docs/design/hooks/callmeter.md
// § Backfill's orphan retirement: an untyped internal sub-agent's pending
// request is retired only when it is old enough, unreachable through a typed
// call or an agents row, and its transcript is missing from every config dir.
func TestRetireOrphansCases(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	configDir := t.TempDir()

	// (a) untyped, >10min, no transcript: retired.
	seedPending(t, store, "pending:toolu_a", "session_a", "agent_a", "", 11*time.Minute)
	// (b) untyped, <10min, no transcript: kept (grace).
	seedPending(t, store, "pending:toolu_b", "session_b", "agent_b", "", 5*time.Minute)
	// (c) typed agent, >10min, no transcript: kept (typed).
	seedPending(t, store, "pending:toolu_c", "session_c", "agent_c", "explore", 11*time.Minute)
	// (d) untyped, >10min, transcript exists: kept.
	seedPending(t, store, "pending:toolu_d", "session_d", "agent_d", "", 11*time.Minute)
	transcript := filepath.Join(configDir, "projects", "slug-d", "session_d", "subagents", "agent-agent_d.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	// (e) main-chat pending request (no agent_id): kept.
	seedPending(t, store, "pending:toolu_e", "session_e", "", "", 11*time.Minute)

	retired, err := retireOrphans(ctx, store, []string{configDir}, orphanNow, t.Logf)
	if err != nil {
		t.Fatalf("retireOrphans: %v", err)
	}
	if retired != 1 {
		t.Errorf("retireOrphans returned %d, want 1", retired)
	}

	if requestExists(t, store, "pending:toolu_a") || callExists(t, store, "toolu_pending:toolu_a") {
		t.Error("(a) old untyped orphan with no transcript should be retired")
	}
	if !requestExists(t, store, "pending:toolu_b") {
		t.Error("(b) an orphan younger than 10min should be kept")
	}
	if !requestExists(t, store, "pending:toolu_c") {
		t.Error("(c) a typed agent with a missing transcript should be kept")
	}
	if !requestExists(t, store, "pending:toolu_d") {
		t.Error("(d) an untyped agent whose transcript exists should be kept")
	}
	if !requestExists(t, store, "pending:toolu_e") {
		t.Error("(e) a main-chat pending request should be kept")
	}
}
