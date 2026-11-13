package store

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
)

func TestPlainQueries(t *testing.T) {
	store := openTestStore(t)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	transcript := Transcript{
		UUID:         "cc-1",
		Path:         "/fixtures/cc-1.jsonl",
		Size:         100,
		MTimeNS:      200,
		ParsedOffset: 90,
		CWD:          "/work/one",
		CustomTitle:  "custom",
		AITitle:      "ai",
		FirstPrompt:  "first",
		LastPrompt:   "last",
		PromptCount:  3,
		IsBG:         true,
	}
	if err := store.UpsertTranscript(ctx, transcript); err != nil {
		t.Fatalf("UpsertTranscript() error = %v", err)
	}
	gotTranscript, found, err := store.Transcript(ctx, transcript.UUID)
	if err != nil {
		t.Fatalf("Transcript() error = %v", err)
	}
	if !found || !reflect.DeepEqual(gotTranscript, transcript) {
		t.Fatalf("Transcript() = %#v, %v, want %#v, true", gotTranscript, found, transcript)
	}
	transcript.LastPrompt = "new last"
	transcript.PromptCount++
	if err := store.UpsertTranscript(ctx, transcript); err != nil {
		t.Fatalf("second UpsertTranscript() error = %v", err)
	}
	transcripts, err := store.Transcripts(ctx)
	if err != nil {
		t.Fatalf("Transcripts() error = %v", err)
	}
	if !reflect.DeepEqual(transcripts, []Transcript{transcript}) {
		t.Fatalf("Transcripts() = %#v, want %#v", transcripts, []Transcript{transcript})
	}

	rollout := Rollout{
		ID:           "cx-1",
		Path:         "/fixtures/cx-1.jsonl",
		Size:         300,
		MTimeNS:      400,
		ParsedOffset: 280,
		CWD:          "/work/two",
		UserThread:   true,
		SessionID:    "session",
		ParentThread: "parent",
		LineageRoot:  "session",
		FirstPrompt:  "hello",
		PromptCount:  5,
	}
	if err := store.UpsertRollout(ctx, rollout); err != nil {
		t.Fatalf("UpsertRollout() error = %v", err)
	}
	gotRollout, found, err := store.Rollout(ctx, rollout.ID)
	if err != nil {
		t.Fatalf("Rollout() error = %v", err)
	}
	if !found || !reflect.DeepEqual(gotRollout, rollout) {
		t.Fatalf("Rollout() = %#v, %v, want %#v, true", gotRollout, found, rollout)
	}
	rollouts, err := store.Rollouts(ctx)
	if err != nil {
		t.Fatalf("Rollouts() error = %v", err)
	}
	if !reflect.DeepEqual(rollouts, []Rollout{rollout}) {
		t.Fatalf("Rollouts() = %#v, want %#v", rollouts, []Rollout{rollout})
	}

	if err := store.ReplaceCxNames(ctx, []CxName{
		{ID: "cx-1", ThreadName: "one"},
		{ID: "cx-2", ThreadName: "two"},
	}); err != nil {
		t.Fatalf("ReplaceCxNames() error = %v", err)
	}
	if err := store.ReplaceCxNames(ctx, []CxName{
		{ID: "cx-2", ThreadName: "renamed"},
	}); err != nil {
		t.Fatalf("second ReplaceCxNames() error = %v", err)
	}
	names, err := store.CxNames(ctx)
	if err != nil {
		t.Fatalf("CxNames() error = %v", err)
	}
	if !reflect.DeepEqual(names, map[string]string{"cx-2": "renamed"}) {
		t.Fatalf("CxNames() = %#v, want cx-2 only", names)
	}

	if err := store.SetMeta(ctx, "cx_index_size", "10"); err != nil {
		t.Fatalf("SetMeta() error = %v", err)
	}
	value, found, err := store.Meta(ctx, "cx_index_size")
	if err != nil {
		t.Fatalf("Meta() error = %v", err)
	}
	if !found || value != "10" {
		t.Fatalf("Meta() = %q, %v, want 10, true", value, found)
	}
	if err := store.DeleteMeta(ctx, "cx_index_size"); err != nil {
		t.Fatalf("DeleteMeta() error = %v", err)
	}
	if _, found, err := store.Meta(ctx, "cx_index_size"); err != nil || found {
		t.Fatalf("Meta() after delete found = %v, error = %v; want false, nil", found, err)
	}

	// A prompt baseline is the /clear ratchet and survives in shared state.
	baseline := int64(7)
	killed := Killed{
		ID:              "cc-1",
		Engine:          "cc",
		KilledAt:        1234,
		BaselinePrompts: &baseline,
	}
	if err := store.Kill(ctx, killed); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}
	gotKilled, found, err := store.Killed(ctx, killed.ID)
	if err != nil {
		t.Fatalf("Killed() error = %v", err)
	}
	wantKilled := killed
	if !found || !reflect.DeepEqual(gotKilled, wantKilled) {
		t.Fatalf("Killed() = %#v, %v, want %#v, true", gotKilled, found, wantKilled)
	}
	if err := store.Kill(ctx, Killed{ID: "cx-1", Engine: "cx", KilledAt: 2345}); err != nil {
		t.Fatalf("Kill() with lazy baseline error = %v", err)
	}
	killedChats, err := store.KilledChats(ctx)
	if err != nil {
		t.Fatalf("KilledChats() error = %v", err)
	}
	if len(killedChats) != 2 || killedChats[1].BaselinePrompts != nil {
		t.Fatalf("KilledChats() = %#v, want two rows and a nil lazy baseline", killedChats)
	}
	if _, err := store.Unkill(ctx, killed.ID); err != nil {
		t.Fatalf("Unkill() error = %v", err)
	}
	if _, found, err := store.Killed(ctx, killed.ID); err != nil || found {
		t.Fatalf("Killed() after unkill found = %v, error = %v; want false, nil", found, err)
	}

	if err := store.DeleteTranscript(ctx, transcript.UUID); err != nil {
		t.Fatalf("DeleteTranscript() error = %v", err)
	}
	if _, found, err := store.Transcript(ctx, transcript.UUID); err != nil || found {
		t.Fatalf("Transcript() after delete found = %v, error = %v; want false, nil", found, err)
	}
	if err := store.DeleteRollout(ctx, rollout.ID); err != nil {
		t.Fatalf("DeleteRollout() error = %v", err)
	}
	if _, found, err := store.Rollout(ctx, rollout.ID); err != nil || found {
		t.Fatalf("Rollout() after delete found = %v, error = %v; want false, nil", found, err)
	}
}

func TestRolloutUpsertRepairsCollapsedIdentityPathConflict(t *testing.T) {
	database := openTestStore(t)
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	path := "/codex/rollout-fork-id.jsonl"
	if err := database.UpsertRollout(ctx, Rollout{
		ID:   "parent-id",
		Path: path,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertRollout(ctx, Rollout{
		ID:   "fork-id",
		Path: path,
	}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.Rollout(ctx, "parent-id"); err != nil || found {
		t.Fatalf("displaced parent found=%t err=%v", found, err)
	}
	repaired, found, err := database.Rollout(ctx, "fork-id")
	if err != nil || !found || repaired.Path != path {
		t.Fatalf("repaired rollout=%#v found=%t err=%v", repaired, found, err)
	}
}

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

	transcripts, rollouts, counts, err := database.DefaultCandidates(ctx, 30, 15)
	if err != nil {
		t.Fatal(err)
	}
	if len(transcripts) != 30 || len(rollouts) != 15 {
		t.Fatalf("candidate lengths=%d/%d, want 30/15", len(transcripts), len(rollouts))
	}
	if counts.Killed != 2 || counts.Suppressed != 18 {
		t.Fatalf("cached counts=%+v, want killed=2 suppressed=18", counts)
	}
	for _, transcript := range transcripts {
		if transcript.IsBG || transcript.Size <= 0 || transcript.PromptCount <= 0 ||
			transcript.UUID == "cc-killed" {
			t.Fatalf("ineligible cached transcript: %#v", transcript)
		}
	}
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
	_, rollouts, counts, err := database.DefaultCandidates(ctx, 10, 10)
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
	_, rollouts, counts, err = database.DefaultCandidates(ctx, 10, 10)
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
	_, rollouts, _, err = database.DefaultCandidates(ctx, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rollouts) != 1 || rollouts[0].LineageRoot != "root" {
		t.Fatalf("unkill did not bring the lineage back: %#v", rollouts)
	}
}

// hasOpenCodeSessionsAssistantCount reports whether oc_sessions carries the
// additive assistant_count column, read directly via PRAGMA table_info so
// the check never depends on the ensure step it is proving.
func hasOpenCodeSessionsAssistantCount(t *testing.T, db *sql.DB) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(oc_sessions)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close rows: %v", err)
		}
	}()
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "assistant_count" {
			return true
		}
	}
	return false
}

// TestFreshStoreEnsuresAssistantCountAtSchema9 pins the cache's v9 migration.
// Since 1-a, older pfm binaries read ~/.local/state/pfm/fleet.db and never
// open pfm-cache.db; the cache is derived, so upgrading it strands no older
// binary.
// assistant_count remains an additive, idempotently ensured column.
func TestFreshStoreEnsuresAssistantCountAtSchema9(t *testing.T) {
	fresh := openTestStore(t)
	t.Cleanup(func() { _ = fresh.Close() })
	got, err := fresh.UserVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != 9 {
		t.Fatalf(
			"UserVersion() = %d, want the hardcoded 9 for the derived cache",
			got,
		)
	}
	if !hasOpenCodeSessionsAssistantCount(t, fresh.db) {
		t.Fatal("fresh store has no oc_sessions.assistant_count column")
	}
}
