package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/store"
)

func TestRefreshCodexLineageFullDeltaAndUnrelatedFiles(t *testing.T) {
	database := openIndexStore(t)
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "rollout-child.jsonl")
	prompt := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}` + "\n"
	meta := `{"type":"session_meta","payload":{"thread_source":"user"}}` + "\n"
	if err := os.WriteFile(path, []byte(meta+prompt+prompt), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertRollout(
		ctx,
		store.Rollout{ID: "child", Path: path, SessionID: "parent", UserThread: true, PromptCount: 1},
	); err != nil {
		t.Fatal(err)
	}
	// A missing unrelated file must not be visited or pruned by a clear.
	if err := database.UpsertRollout(
		ctx,
		store.Rollout{ID: "other", Path: filepath.Join(t.TempDir(), "missing.jsonl"), UserThread: true, PromptCount: 7},
	); err != nil {
		t.Fatal(err)
	}
	for want := int64(2); want <= 3; want++ {
		if err := RefreshCodexLineage(ctx, database, "child"); err != nil {
			t.Fatal(err)
		}
		lineage, found, err := database.CodexLineage(ctx, "child")
		if err != nil || !found || lineage.RootID != "parent" || lineage.PromptCount != want {
			t.Fatalf("lineage=%#v found=%v err=%v want prompts=%d", lineage, found, err, want)
		}
		// Repeating a refresh must not double-count the same tail.
		if err := RefreshCodexLineage(ctx, database, "child"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(meta+prompt+prompt+prompt), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	other, found, err := database.Rollout(ctx, "other")
	if err != nil || !found || other.PromptCount != 7 {
		t.Fatalf("unrelated row changed: %#v %v %v", other, found, err)
	}
}
