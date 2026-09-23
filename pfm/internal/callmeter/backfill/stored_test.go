package backfill

import (
	"context"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

// TestStoredPendingNamesOnlyProvisionalKeys: a stored call keyed to a message
// id, a call not yet stored and a transcript call naming no message are never
// pending; two calls of one provisional key name it once.
func TestStoredPendingNamesOnlyProvisionalKeys(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	provisional := callmeter.ProvisionalKey("toolu_1")
	stored := map[string]string{"toolu_1": provisional, "toolu_2": provisional, "toolu_3": "msg_done"}
	for id, request := range stored {
		if err := store.UpsertCall(ctx, callmeter.Call{
			ToolUseID: id,
			RequestID: callmeter.Ptr(request),
		}, callmeter.Overwrite); err != nil {
			t.Fatalf("seed call %s: %v", id, err)
		}
	}
	calls := []*callmeter.Call{
		{ToolUseID: "toolu_1", RequestID: callmeter.Ptr("msg_A")},
		{ToolUseID: "toolu_2", RequestID: callmeter.Ptr("msg_A")},
		{ToolUseID: "toolu_3", RequestID: callmeter.Ptr("msg_done")},
		{ToolUseID: "toolu_4", RequestID: callmeter.Ptr("msg_B")},
		{ToolUseID: "toolu_5"},
	}
	keys, err := storedPending(ctx, store, calls)
	if err != nil {
		t.Fatalf("storedPending: %v", err)
	}
	if len(keys.order) != 1 || keys.order[0] != provisional || keys.message[provisional] != "msg_A" {
		t.Errorf("storedPending = %v %v, want [%s] -> msg_A", keys.order, keys.message, provisional)
	}
}
