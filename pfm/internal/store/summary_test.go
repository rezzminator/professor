package store

import (
	"context"
	"testing"
)

func TestChatSummaryCacheIsKeyedByTranscriptPathAndOffset(t *testing.T) {
	database := openTestStore(t)
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()

	if summary, found, err := database.ChatSummary(
		ctx,
		"/fixture/chat.jsonl",
		41,
	); err != nil || found ||
		summary != "" {
		t.Fatalf("empty cache summary=%q found=%t err=%v", summary, found, err)
	}
	if err := database.PutChatSummary(ctx, "/fixture/chat.jsonl", 41, "first exchange"); err != nil {
		t.Fatal(err)
	}
	if summary, found, err := database.ChatSummary(
		ctx,
		"/fixture/chat.jsonl",
		41,
	); err != nil || !found ||
		summary != "first exchange" {
		t.Fatalf("cache summary=%q found=%t err=%v", summary, found, err)
	}
	for _, key := range []struct {
		path   string
		offset int64
	}{
		{path: "/fixture/chat.jsonl", offset: 42},
		{path: "/fixture/other.jsonl", offset: 41},
	} {
		if summary, found, err := database.ChatSummary(
			ctx,
			key.path,
			key.offset,
		); err != nil || found ||
			summary != "" {
			t.Fatalf("key %+v leaked summary=%q found=%t err=%v", key, summary, found, err)
		}
	}
}
