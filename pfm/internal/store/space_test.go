package store

import (
	"context"
	"reflect"
	"testing"
)

// TestChatSpacesTieGoesToTheRecordUnderTheChatsOwnKey pins the tie between a
// continued Claude chat's records made in the same second: the one recorded
// under the tail, the id the chat now runs and resumes by, wins under every id,
// whichever id sorts first; no record at all is an empty map, never nil.
func TestChatSpacesTieGoesToTheRecordUnderTheChatsOwnKey(t *testing.T) {
	for _, test := range []struct {
		name, root, mid, tail string // mid: a segment between root and tail with no record of its own
		want                  map[string]string
	}{
		{name: "root sorts first", root: "a-root", tail: "z-tail", want: map[string]string{"a-root": "epic", "z-tail": "epic"}},
		{
			name: "an unrecorded middle segment wears the space", root: "a-root", mid: "m-mid", tail: "z-tail",
			want: map[string]string{"a-root": "epic", "m-mid": "epic", "z-tail": "epic"},
		},
		{name: "tail sorts first", root: "z-root", tail: "a-tail", want: map[string]string{"z-root": "epic", "a-tail": "epic"}},
		{name: "none recorded", want: map[string]string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			database, err := OpenMemory()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			ctx := context.Background()
			if test.root != "" {
				chain := []Transcript{{UUID: test.root, ContinuedIn: test.tail}, {UUID: test.tail}}
				if test.mid != "" {
					chain = append(chain[:1:1], Transcript{UUID: test.mid, ContinuedIn: test.tail}, chain[1])
					chain[0].ContinuedIn = test.mid
				}
				for _, transcript := range chain {
					transcript.Path = "/fixture/" + transcript.UUID + ".jsonl"
					if err := database.UpsertTranscript(ctx, transcript); err != nil {
						t.Fatal(err)
					}
				}
				for id, space := range map[string]string{test.root: "glass", test.tail: "epic"} {
					if err := database.Shared().SetSpace(ctx, id, space, 5); err != nil {
						t.Fatal(err)
					}
				}
			}
			got, err := database.ChatSpaces(ctx)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ChatSpaces = %#v, %v; want %#v", got, err, test.want)
			}
		})
	}
}
