package store

import (
	"context"
	"testing"
)

// TestContinuationTailFollowsOnlyIndexedHandoffs: a resume of a superseded
// segment must name the newest one; a handoff to a successor the index lacks
// is not followed, an unknown id is its own tail, and a cycle stops.
func TestContinuationTailFollowsOnlyIndexedHandoffs(t *testing.T) {
	opened := openTestStore(t)
	ctx := context.Background()
	for _, transcript := range []Transcript{
		{UUID: "first", Path: "/p/first.jsonl", Size: 1, PromptCount: 1, ContinuedIn: "second"},
		{UUID: "second", Path: "/p/second.jsonl", Size: 1, PromptCount: 1, ContinuedIn: "third"},
		{UUID: "third", Path: "/p/third.jsonl", Size: 1, PromptCount: 1},
		{UUID: "orphan", Path: "/p/orphan.jsonl", Size: 1, PromptCount: 1, ContinuedIn: "gone"},
		{UUID: "loop-a", Path: "/p/loop-a.jsonl", Size: 1, PromptCount: 1, ContinuedIn: "loop-b"},
		{UUID: "loop-b", Path: "/p/loop-b.jsonl", Size: 1, PromptCount: 1, ContinuedIn: "loop-a"},
	} {
		if err := opened.UpsertTranscript(ctx, transcript); err != nil {
			t.Fatal(err)
		}
	}
	for start, want := range map[string]string{
		"first": "third", "second": "third", "third": "third",
		"orphan": "orphan", "unknown": "unknown", "loop-a": "loop-b",
	} {
		if got, err := opened.ContinuationTail(ctx, start); err != nil || got != want {
			t.Fatalf("ContinuationTail(%s) = %q, %v; want %q", start, got, err, want)
		}
	}
}
