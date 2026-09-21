package fleet

import (
	"context"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// TestRolloutIDFromPathStripsTheTimestampPrefix pins the rollout file name
// grammar: the thread id is what follows the 19-character timestamp.
func TestRolloutIDFromPathStripsTheTimestampPrefix(t *testing.T) {
	for _, test := range []struct{ path, want string }{
		{"/r/2026/01/02/rollout-2026-01-02T03-04-05-0199aaaa-bbbb-7ccc-8ddd-eeeeeeeeeeee.jsonl", "0199aaaa-bbbb-7ccc-8ddd-eeeeeeeeeeee"},
		{"rollout-short.jsonl", "short"},
		{"", ""},
	} {
		if got := rolloutIDFromPath(test.path); got != test.want {
			t.Errorf("rolloutIDFromPath(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}

// TestEnrichLiveLeavesCompleteDataUntouched pins the no-op path: when every
// live chat is already loaded, enrichment asks the index nothing (the nil
// database would fail the call) and returns the data as given. A rollout-less
// Codex process has no id to look up at all.
func TestEnrichLiveLeavesCompleteDataUntouched(t *testing.T) {
	data := Data{
		Transcripts: []store.Transcript{{UUID: "claude-1"}},
		Rollouts:    []store.Rollout{{ID: "codex-1"}},
	}
	live := gather.Snapshot{
		Agents: []gather.Agent{{SessionID: "claude-1"}},
		Codex:  []gather.LiveCodex{{RolloutPath: "rollout-2026-01-02T03-04-05-codex-1.jsonl"}, {RolloutPath: ""}},
	}
	got, err := EnrichLive(context.Background(), nil, data, live)
	if err != nil {
		t.Fatalf("EnrichLive() = %v", err)
	}
	if len(got.Transcripts) != 1 || len(got.Rollouts) != 1 {
		t.Fatalf("EnrichLive() = %+v, want the data unchanged", got)
	}
}
