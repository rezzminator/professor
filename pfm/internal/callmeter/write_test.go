package callmeter

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// postToolUse and batch are the two events that write one call: the hook's
// PostToolUse owns the call's facts, PostToolBatch its delivered bytes and
// request. Async hooks land them in either order.
func postToolUse() Call {
	return Call{
		ToolUseID:  "toolu_1",
		SessionID:  Ptr("sess-1"),
		TS:         Ptr(int64(1000)),
		Tool:       Ptr("Bash"),
		Input:      Ptr(`{"command":"seq 1 5"}`),
		Cwd:        Ptr("/tmp/demo-proj"),
		DurationMS: Ptr(int64(12)),
		Failed:     Ptr(false),
		BytesReal:  Ptr(int64(10)),
		Source:     Ptr(SourceHook),
	}
}

func batch() Call {
	return Call{
		ToolUseID:      "toolu_1",
		SessionID:      Ptr("sess-1"),
		RequestID:      Ptr(ProvisionalKey("toolu_1")),
		BytesDelivered: Ptr(int64(10)),
		Source:         Ptr(SourceHook),
	}
}

func TestUpsertOrderIndependence(t *testing.T) {
	ctx := context.Background()
	orders := map[string][]Call{
		"post then batch": {postToolUse(), batch()},
		"batch then post": {batch(), postToolUse()},
	}
	rows := map[string]map[string]any{}
	for name, calls := range orders {
		store := openTestStore(t)
		for _, c := range calls {
			if err := store.UpsertCall(ctx, c, Overwrite); err != nil {
				t.Fatalf("%s: UpsertCall: %v", name, err)
			}
		}
		rows[name] = row(t, store, "calls", "tool_use_id = ?", "toolu_1")
	}
	a, b := rows["post then batch"], rows["batch then post"]
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("rows differ by arrival order:\npost first:  %v\nbatch first: %v", a, b)
	}
	if a["bytes_delivered"] != int64(10) || a["tool"] != "Bash" || a["request_id"] != "pending:toolu_1" {
		t.Fatalf("merged row lost a column: %v", a)
	}
	if a["file_bytes"] != nil {
		t.Fatalf("unprovided file_bytes = %v, want NULL", a["file_bytes"])
	}
}

func TestFillEmptyNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.UpsertCall(ctx, postToolUse(), Overwrite); err != nil {
		t.Fatal(err)
	}
	fill := Call{
		ToolUseID:      "toolu_1",
		TS:             Ptr(int64(999)),
		Tool:           Ptr("Read"),
		BytesDelivered: Ptr(int64(42)),
		Source:         Ptr(SourceHook),
	}
	if err := store.UpsertCall(ctx, fill, FillEmpty); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCall(ctx, Call{ToolUseID: "toolu_2", Tool: Ptr("Grep")}, FillEmpty); err != nil {
		t.Fatal(err)
	}
	got := row(t, store, "calls", "tool_use_id = ?", "toolu_1")
	want := map[string]any{"ts": int64(1000), "tool": "Bash", "source": SourceHook, "bytes_delivered": int64(42)}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %v, want %v", name, got[name], value)
		}
	}
	if inserted := row(t, store, "calls", "tool_use_id = ?", "toolu_2"); inserted == nil || inserted["tool"] != "Grep" {
		t.Errorf("FillEmpty did not insert the missing row: %v", inserted)
	}
}

func TestUpsertCutsError(t *testing.T) {
	store := openTestStore(t)
	if err := store.UpsertCall(
		context.Background(),
		Call{ToolUseID: "toolu_1", Error: Ptr(strings.Repeat("é", 600))},
		Overwrite,
	); err != nil {
		t.Fatal(err)
	}
	got, _ := row(t, store, "calls", "tool_use_id = ?", "toolu_1")["error"].(string)
	if n := len([]rune(got)); n != ErrorLimit {
		t.Fatalf("stored error holds %d characters, want %d", n, ErrorLimit)
	}
}

func TestResolveRequestRewritesCallsAndMerges(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	// Two batches of one message: the first already resolved to msg_1, the
	// second still provisional under its first call.
	if err := store.Batch(ctx, func(tx *Tx) error {
		if err := tx.UpsertRequest(
			ctx,
			Request{
				RequestID:     "msg_1",
				SessionID:     Ptr("sess-1"),
				TS:            Ptr(int64(2000)),
				ContextTokens: Ptr(int64(500)),
				Calls:         Ptr(int64(1)),
				Pending:       Ptr(false),
			},
			Overwrite,
		); err != nil {
			return err
		}
		if err := tx.UpsertCall(ctx, Call{ToolUseID: "toolu_a", RequestID: Ptr("msg_1")}, Overwrite); err != nil {
			return err
		}
		key := ProvisionalKey("toolu_b")
		if err := tx.UpsertRequest(
			ctx,
			Request{
				RequestID: key,
				SessionID: Ptr("sess-1"),
				TS:        Ptr(int64(1500)),
				Calls:     Ptr(int64(2)),
				Pending:   Ptr(true),
				ConfigDir: Ptr("/tmp/demo-config"),
			},
			Overwrite,
		); err != nil {
			return err
		}
		for _, id := range []string{"toolu_b", "toolu_c"} {
			if err := tx.UpsertCall(ctx, Call{ToolUseID: id, RequestID: Ptr(key)}, Overwrite); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.ResolveRequest(
		ctx,
		ProvisionalKey("toolu_b"),
		Request{RequestID: "msg_1", OutputTokens: Ptr(int64(30))},
	); err != nil {
		t.Fatalf("ResolveRequest: %v", err)
	}
	if n := count(t, store, "requests"); n != 1 {
		t.Fatalf("requests holds %d rows, want the one merged msg_1", n)
	}
	got := row(t, store, "requests", "request_id = ?", "msg_1")
	want := map[string]any{
		"calls":          int64(3),
		"ts":             int64(1500),
		"context_tokens": int64(500),
		"output_tokens":  int64(30),
		"pending":        int64(0),
		"config_dir":     "/tmp/demo-config",
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("merged %s = %v, want %v", name, got[name], value)
		}
	}
	for _, id := range []string{"toolu_a", "toolu_b", "toolu_c"} {
		if c := row(t, store, "calls", "tool_use_id = ?", id); c["request_id"] != "msg_1" {
			t.Errorf("call %s request_id = %v, want msg_1", id, c["request_id"])
		}
	}
}

func TestResolveRequestWithoutExistingMessageRow(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	key := ProvisionalKey("toolu_x")
	if err := store.UpsertRequest(
		ctx,
		Request{RequestID: key, SessionID: Ptr("sess-1"), Calls: Ptr(int64(1)), Pending: Ptr(true)},
		Overwrite,
	); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCall(ctx, Call{ToolUseID: "toolu_x", RequestID: Ptr(key)}, Overwrite); err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveRequest(ctx, key, Request{RequestID: "msg_9", ContextTokens: Ptr(int64(7))}); err != nil {
		t.Fatal(err)
	}
	got := row(t, store, "requests", "request_id = ?", "msg_9")
	if got == nil || got["session_id"] != "sess-1" || got["calls"] != int64(1) || got["pending"] != int64(0) ||
		got["context_tokens"] != int64(7) {
		t.Fatalf("resolved row = %v", got)
	}
	if row(t, store, "requests", "request_id = ?", key) != nil {
		t.Fatal("the provisional row survived")
	}
}

func TestPendingRequests(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	seed := []Request{
		{RequestID: ProvisionalKey("toolu_main"), SessionID: Ptr("sess-1"), TS: Ptr(int64(1)), Pending: Ptr(true)},
		{
			RequestID: ProvisionalKey("toolu_sub"),
			SessionID: Ptr("sess-1"),
			AgentID:   Ptr("agent-7"),
			TS:        Ptr(int64(2)),
			Pending:   Ptr(true),
		},
		{RequestID: "msg_done", SessionID: Ptr("sess-1"), Pending: Ptr(false)},
	}
	for _, r := range seed {
		if err := store.UpsertRequest(ctx, r, Overwrite); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []Call{
		{ToolUseID: "toolu_main", RequestID: Ptr(ProvisionalKey("toolu_main")), TS: Ptr(int64(1))},
		{ToolUseID: "toolu_main2", RequestID: Ptr(ProvisionalKey("toolu_main")), TS: Ptr(int64(2))},
	} {
		if err := store.UpsertCall(ctx, c, Overwrite); err != nil {
			t.Fatal(err)
		}
	}
	main, err := store.PendingRequests(ctx, "sess-1", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []PendingRequest{{RequestID: "pending:toolu_main", CallIDs: []string{"toolu_main", "toolu_main2"}}}
	if !reflect.DeepEqual(main, want) {
		t.Fatalf("main chat pending = %v, want %v", main, want)
	}
	sub, err := store.PendingRequests(ctx, "sess-1", "agent-7")
	if err != nil {
		t.Fatal(err)
	}
	// No call row yet: the key's own first call is still listed.
	want = []PendingRequest{{RequestID: "pending:toolu_sub", CallIDs: []string{"toolu_sub"}}}
	if !reflect.DeepEqual(sub, want) {
		t.Fatalf("agent pending = %v, want %v", sub, want)
	}
}

func TestReplaceCommandParts(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	first := []CommandPart{
		{
			Seq:         0,
			Lang:        "sh",
			Program:     "cat",
			Args:        []string{"a.txt"},
			Files:       []string{"/tmp/demo-proj/a.txt"},
			ParseStatus: "ok",
			Conditional: true,
		},
		{Seq: 1, Lang: "sh", Program: "wc"},
	}
	if err := store.ReplaceCommandParts(ctx, "toolu_1", first); err != nil {
		t.Fatal(err)
	}
	if got := row(t, store, "command_parts", "tool_use_id = ? AND seq = 1", "toolu_1"); got["conditional"] != int64(0) {
		t.Fatalf("certain part stored conditional = %v, want 0", got["conditional"])
	}
	if err := store.ReplaceCommandParts(ctx, "toolu_1", first[:1]); err != nil {
		t.Fatal(err)
	}
	if n := count(t, store, "command_parts"); n != 1 {
		t.Fatalf("command_parts holds %d rows after replace, want 1", n)
	}
	got := row(t, store, "command_parts", "tool_use_id = ? AND seq = 0", "toolu_1")
	if got["args"] != `["a.txt"]` || got["files"] != `["/tmp/demo-proj/a.txt"]` || got["program"] != "cat" ||
		got["conditional"] != int64(1) {
		t.Fatalf("part = %v", got)
	}
}

func TestUpsertRejectsEmptyKey(t *testing.T) {
	store := openTestStore(t)
	err := store.UpsertCall(context.Background(), Call{Tool: Ptr("Bash")}, Overwrite)
	if err == nil || !strings.Contains(err.Error(), "tool_use_id") {
		t.Fatalf("UpsertCall without a key = %v, want an error naming tool_use_id", err)
	}
}
