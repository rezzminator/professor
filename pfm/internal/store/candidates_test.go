package store

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestDefaultCandidatesAreCappedAndCountsStayHonest(t *testing.T) {
	database := openTestStore(t)
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	ctx := context.Background()
	for index := 0; index < 35; index++ {
		if err := database.UpsertTranscript(ctx, Transcript{
			UUID:        fmt.Sprintf("cc-%02d", index),
			Path:        fmt.Sprintf("/cc/%02d.jsonl", index),
			Size:        100,
			MTimeNS:     int64(index),
			CWD:         "/work/cc",
			PromptCount: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 5; index++ {
		if err := database.UpsertTranscript(ctx, Transcript{
			UUID:        fmt.Sprintf("bg-%02d", index),
			Path:        fmt.Sprintf("/cc/bg-%02d.jsonl", index),
			Size:        100,
			CWD:         "/work/bg",
			PromptCount: 1,
			IsBG:        true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 2; index++ {
		if err := database.UpsertTranscript(ctx, Transcript{
			UUID: fmt.Sprintf("empty-%02d", index),
			Path: fmt.Sprintf("/cc/empty-%02d.jsonl", index),
			CWD:  "/work/empty",
		}); err != nil {
			t.Fatal(err)
		}
	}
	current := int64(2)
	if err := database.UpsertTranscript(ctx, Transcript{
		UUID: "cc-killed", Path: "/cc/killed.jsonl", Size: 100,
		MTimeNS: 100, CWD: "/work/cc", PromptCount: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Kill(ctx, Killed{
		ID: "cc-killed", Engine: "cc", BaselinePrompts: &current,
	}); err != nil {
		t.Fatal(err)
	}
	// cc-grown carries a /clear baseline its prompt count already passed, so
	// the cached frame must auto-unkill it.
	stale := int64(1)
	if err := database.UpsertTranscript(ctx, Transcript{
		UUID: "cc-grown", Path: "/cc/grown.jsonl", Size: 100,
		MTimeNS: 101, CWD: "/work/cc", PromptCount: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Kill(ctx, Killed{
		ID: "cc-grown", Engine: "cc", BaselinePrompts: &stale,
	}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 20; index++ {
		if err := database.UpsertRollout(ctx, Rollout{
			ID:          fmt.Sprintf("cx-%02d", index),
			Path:        fmt.Sprintf("/cx/%02d.jsonl", index),
			Size:        100,
			MTimeNS:     int64(index),
			CWD:         "/work/cx",
			UserThread:  true,
			PromptCount: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.UpsertRollout(ctx, Rollout{
		ID: "cx-killed", Path: "/cx/killed.jsonl", Size: 100,
		MTimeNS: 100, CWD: "/work/cx", UserThread: true, PromptCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Kill(ctx, Killed{
		ID: "cx-killed", Engine: "cx",
	}); err != nil {
		t.Fatal(err)
	}

	// Newest first: cc-grown (auto-unkilled) above cc-34…cc-00, cx-19…cx-00;
	// cc-killed and cx-killed stay out, bg-* and empty-* are suppressed.
	tests := []struct {
		name            string
		claims          LiveClaims
		wantTranscripts []string
		wantRollouts    []string
		wantCounts      CachedCounts
	}{
		{
			name:            "no live chat",
			wantTranscripts: append([]string{"cc-grown"}, descendingIDs("cc", 34, 6)...),
			wantRollouts:    descendingIDs("cx", 19, 5),
			wantCounts:      CachedCounts{Killed: 2, Suppressed: 18},
		},
		{
			name: "a claim on no candidate",
			claims: LiveClaims{
				Transcripts: map[string]struct{}{"cc-absent": {}},
				Rollouts:    map[string]struct{}{"cx-absent": {}},
			},
			wantTranscripts: append([]string{"cc-grown"}, descendingIDs("cc", 34, 6)...),
			wantRollouts:    descendingIDs("cx", 19, 5),
			wantCounts:      CachedCounts{Killed: 2, Suppressed: 18},
		},
		{
			// A live chat keeps its row but frees its resume slot: two more
			// old Claude chats and one more old Codex chat fill the caps,
			// and the overflow shrinks by the same three.
			name: "live chats among the newest",
			claims: LiveClaims{
				Transcripts: map[string]struct{}{"cc-34": {}, "cc-20": {}},
				Rollouts:    map[string]struct{}{"cx-19": {}},
			},
			wantTranscripts: append([]string{"cc-grown"}, descendingIDs("cc", 34, 4)...),
			wantRollouts:    descendingIDs("cx", 19, 4),
			wantCounts:      CachedCounts{Killed: 2, Suppressed: 15},
		},
		{
			// Compose counts a live row itself, killed or empty: a claimed
			// chat drops out of the totals it would otherwise land in.
			name: "killed and empty chats shown live",
			claims: LiveClaims{
				Transcripts: map[string]struct{}{"cc-killed": {}, "empty-00": {}},
				Rollouts:    map[string]struct{}{"cx-killed": {}},
			},
			wantTranscripts: append([]string{"cc-grown"}, descendingIDs("cc", 34, 6)...),
			wantRollouts:    descendingIDs("cx", 19, 5),
			wantCounts:      CachedCounts{Killed: 0, Suppressed: 17},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transcripts, rollouts, counts, err := database.DefaultCandidates(ctx, 30, 15, test.claims)
			if err != nil {
				t.Fatal(err)
			}
			gotTranscripts := make([]string, 0, len(transcripts))
			for _, transcript := range transcripts {
				gotTranscripts = append(gotTranscripts, transcript.UUID)
			}
			if !reflect.DeepEqual(gotTranscripts, test.wantTranscripts) {
				t.Errorf("transcripts =\n%v\nwant\n%v", gotTranscripts, test.wantTranscripts)
			}
			gotRollouts := make([]string, 0, len(rollouts))
			for _, rollout := range rollouts {
				gotRollouts = append(gotRollouts, rollout.ID)
			}
			if !reflect.DeepEqual(gotRollouts, test.wantRollouts) {
				t.Errorf("rollouts =\n%v\nwant\n%v", gotRollouts, test.wantRollouts)
			}
			if counts != test.wantCounts {
				t.Errorf("cached counts = %+v, want %+v", counts, test.wantCounts)
			}
		})
	}
}

// descendingIDs names prefix-from down to prefix-to, the fixture's ids
// newest first.
func descendingIDs(prefix string, from, to int) []string {
	ids := make([]string, 0, from-to+1)
	for index := from; index >= to; index-- {
		ids = append(ids, fmt.Sprintf("%s-%02d", prefix, index))
	}
	return ids
}

// TestDefaultRolloutsKilledOnAnyLineageMember is codexLineageKilled's own
// fixture: a kill keyed on a MEMBER id — never the lineage root — must still pull the
// whole conversation out of the cached first frame. "child-old" is neither
// the root nor the newest file, so a fix that only special-cases those two
// would still miss it.
func TestDefaultRolloutsKilledOnAnyLineageMember(t *testing.T) {
	database := openTestStore(t)
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	ctx := context.Background()

	for _, rollout := range lineageFixtureRollouts() {
		if err := database.UpsertRollout(ctx, rollout); err != nil {
			t.Fatal(err)
		}
	}
	_, rollouts, counts, err := database.DefaultCandidates(ctx, 10, 10, LiveClaims{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rollouts) != 1 || rollouts[0].LineageRoot != "root" {
		t.Fatalf("unkilled lineage = %#v, want exactly the root-lineage row", rollouts)
	}
	if counts.Killed != 0 {
		t.Fatalf("counts before kill = %+v, want zero killed", counts)
	}

	if err := database.Kill(ctx, Killed{ID: "child-old", Engine: "cx"}); err != nil {
		t.Fatal(err)
	}
	_, rollouts, counts, err = database.DefaultCandidates(ctx, 10, 10, LiveClaims{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rollouts) != 0 {
		t.Fatalf(
			"a kill on a non-root, non-newest member left the lineage listed: %#v",
			rollouts,
		)
	}
	if counts.Killed != 1 {
		t.Fatalf("counts after member kill = %+v, want killed=1", counts)
	}

	if _, err := database.Unkill(ctx, "child-old"); err != nil {
		t.Fatal(err)
	}
	_, rollouts, _, err = database.DefaultCandidates(ctx, 10, 10, LiveClaims{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rollouts) != 1 || rollouts[0].LineageRoot != "root" {
		t.Fatalf("unkill did not bring the lineage back: %#v", rollouts)
	}

	// A live claim on that same non-root member frees the lineage's resume
	// slot: with no slot at all it is still returned and never overflows.
	for _, claim := range []struct {
		claimed        map[string]struct{}
		wantRollouts   int
		wantSuppressed int
	}{
		{claimed: nil, wantRollouts: 0, wantSuppressed: 1},
		{claimed: map[string]struct{}{"child-old": {}}, wantRollouts: 1, wantSuppressed: 0},
	} {
		_, rollouts, counts, err = database.DefaultCandidates(ctx, 10, 0, LiveClaims{Rollouts: claim.claimed})
		if err != nil {
			t.Fatal(err)
		}
		if len(rollouts) != claim.wantRollouts || counts.Suppressed != claim.wantSuppressed {
			t.Errorf("claims %v: rollouts %#v, suppressed %d; want %d rollouts, suppressed %d",
				claim.claimed, rollouts, counts.Suppressed, claim.wantRollouts, claim.wantSuppressed)
		}
	}
}
