package store

import (
	"context"
	"io"
	"testing"
)

func TestSessionLabel(t *testing.T) {
	setStoreTestJail(t)
	database := openTestStore(t)
	ctx := context.Background()
	for _, transcript := range []Transcript{
		{UUID: "named", Path: "/fixtures/named.jsonl", CustomTitle: "custom", AITitle: "ai", FirstPrompt: "first"},
		{UUID: "ai-titled", Path: "/fixtures/ai.jsonl", AITitle: "ai", FirstPrompt: "first"},
		{UUID: "prompted", Path: "/fixtures/prompted.jsonl", FirstPrompt: "first"},
	} {
		if err := database.UpsertTranscript(ctx, transcript); err != nil {
			t.Fatalf("UpsertTranscript(%s) error = %v", transcript.UUID, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	for id, want := range map[string]string{
		"named":     "custom",
		"ai-titled": "ai",
		"prompted":  "first",
		"unknown":   "",
	} {
		got, err := SessionLabel(ctx, id, WithWarningWriter(io.Discard))
		if err != nil || got != want {
			t.Fatalf("SessionLabel(%q) = %q, %v; want %q, nil", id, got, err, want)
		}
	}
}
