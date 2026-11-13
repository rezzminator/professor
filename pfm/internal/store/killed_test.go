package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	helperProcessEnv = "PFM_STORE_HELPER"
	helperPrefixEnv  = "PFM_STORE_HELPER_PREFIX"
	helperReadyEnv   = "PFM_STORE_HELPER_READY"
	helperGateEnv    = "PFM_STORE_HELPER_GATE"
	helperWriteCount = 200
)

func TestPromptBaselineKillAutoUnkillsAfterTranscriptGrowth(t *testing.T) {
	database := openTestStore(t)
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	id := "clear-killed"
	if err := database.UpsertTranscript(ctx, Transcript{
		UUID: id, Path: "/claude/clear-killed.jsonl", PromptCount: 2,
	}); err != nil {
		t.Fatal(err)
	}
	baseline := int64(2)
	if err := database.Kill(ctx, Killed{
		ID: id, Engine: pfmengine.Claude, KilledAt: 10, BaselinePrompts: &baseline,
	}); err != nil {
		t.Fatal(err)
	}
	killed, found, err := database.Killed(ctx, id)
	if err != nil || !found || killed.BaselinePrompts == nil || *killed.BaselinePrompts != baseline {
		t.Fatalf("baseline kill = %#v found=%v error=%v", killed, found, err)
	}
	if err := database.UpsertTranscript(ctx, Transcript{
		UUID: id, Path: "/claude/clear-killed.jsonl", PromptCount: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if killed, found, err = database.Killed(ctx, id); err != nil || found {
		t.Fatalf("grown baseline kill = %#v found=%v error=%v", killed, found, err)
	}
	raw, err := database.state.KilledAt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := raw[id]; found {
		t.Fatalf("expired baseline kill remains in shared state: %#v", raw)
	}
}

type killedRowFixture struct {
	id     string
	engine string
}

func (row killedRowFixture) Scan(dest ...any) error {
	*dest[0].(*string) = row.id
	*dest[1].(*string) = row.engine
	*dest[2].(*int64) = 1
	*dest[3].(*sql.NullInt64) = sql.NullInt64{}
	return nil
}

func TestDatabaseEngineEdgeRejectsUnknownWithAcceptedSet(t *testing.T) {
	_, err := scanKilled(killedRowFixture{id: "row-7", engine: "bogus"})
	if err == nil ||
		!strings.Contains(
			err.Error(),
			`pfm.db row row-7: unknown engine "bogus" (want cc/claude, cx/codex, ox/opencode)`,
		) {
		t.Fatalf("scanKilled(bogus) error = %v", err)
	}
}

func TestKilledChatsDeriveOpenCodeEngineFromMirror(t *testing.T) {
	database := openTestStore(t)
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	const id = "ses-opencode"
	if err := database.ReplaceOpenCodeSessions(ctx, []OpenCodeSession{{ID: id, Title: "fixture"}}); err != nil {
		t.Fatal(err)
	}
	if err := database.Kill(ctx, Killed{ID: id, Engine: pfmengine.OpenCode, KilledAt: 1}); err != nil {
		t.Fatal(err)
	}
	rows, err := database.KilledChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Engine != pfmengine.OpenCode {
		t.Fatalf("KilledChats()=%#v, want one OpenCode row", rows)
	}
	counts, err := database.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.OrphanedKills != 0 {
		t.Fatalf("live mirrored OpenCode kill counted as orphan: %+v", counts)
	}
}

func TestOpenCodeKillBecomesPrunableOnlyAfterMirrorRemoval(t *testing.T) {
	database := openTestStore(t)
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	const id = "ses-opencode-prunable"
	if err := database.ReplaceOpenCodeSessions(ctx, []OpenCodeSession{{ID: id, Title: "fixture"}}); err != nil {
		t.Fatal(err)
	}
	if err := database.Kill(ctx, Killed{ID: id, Engine: pfmengine.OpenCode, KilledAt: 1}); err != nil {
		t.Fatal(err)
	}
	if counts, err := database.Counts(ctx); err != nil || counts.OrphanedKills != 0 {
		t.Fatalf("mirrored OpenCode kill counts=%+v err=%v, want non-orphan", counts, err)
	}
	if err := database.ReplaceOpenCodeSessions(ctx, nil); err != nil {
		t.Fatal(err)
	}
	counts, err := database.Counts(ctx)
	if err != nil || counts.OrphanedKills != 1 {
		t.Fatalf("unmirrored OpenCode kill counts=%+v err=%v, want one orphan", counts, err)
	}
	orphans, err := database.OrphanedKills(ctx)
	if err != nil || len(orphans) != 1 || orphans[0].ID != id {
		t.Fatalf("OrphanedKills()=%#v err=%v, want %q", orphans, err, id)
	}
	deleted, err := database.DeleteOrphanedKills(ctx)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteOrphanedKills()=%d err=%v, want one deletion", deleted, err)
	}
	if _, found, err := database.Killed(ctx, id); err != nil || found {
		t.Fatalf("Killed(%q) found=%t err=%v after prune, want absent", id, found, err)
	}
}

func TestOrphanedKillsListMatchesCountsAndPrunes(t *testing.T) {
	database := openTestStore(t)
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()

	if err := database.UpsertTranscript(ctx, Transcript{
		UUID: "live-cc",
		Path: "/jail/live-cc.jsonl",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertRollout(ctx, Rollout{
		ID:          "live-cx",
		Path:        "/jail/live-cx.jsonl",
		LineageRoot: "lineage-root",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertCxName(ctx, CxName{
		ID:         "named-only-cx",
		ThreadName: "named",
	}); err != nil {
		t.Fatal(err)
	}
	live := []Killed{
		{ID: "live-cc", Engine: "cc", KilledAt: 1},
		{ID: "live-cx", Engine: "cx", KilledAt: 2},
		{ID: "lineage-root", Engine: "cx", KilledAt: 3},
	}
	orphaned := []Killed{
		{ID: "dead-cc", Engine: "cc", KilledAt: 4},
		{ID: "dead-cx", Engine: "cx", KilledAt: 5},
		{ID: "named-only-cx", Engine: "cx", KilledAt: 6},
	}
	for _, killed := range append(append([]Killed(nil), live...), orphaned...) {
		if err := database.Kill(ctx, killed); err != nil {
			t.Fatalf("Kill(%s) error = %v", killed.ID, err)
		}
	}

	counts, err := database.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	orphans, err := database.OrphanedKills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != counts.OrphanedKills {
		t.Fatalf(
			"OrphanedKills() = %d rows, Counts().OrphanedKills = %d; want the same predicate",
			len(orphans),
			counts.OrphanedKills,
		)
	}
	got := make([]string, 0, len(orphans))
	for _, orphan := range orphans {
		got = append(got, orphan.ID)
	}
	want := []string{"dead-cc", "dead-cx", "named-only-cx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OrphanedKills() ids = %v, want %v", got, want)
	}

	deleted, err := database.DeleteOrphanedKills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != len(want) {
		t.Fatalf("DeleteOrphanedKills() = %d, want %d", deleted, len(want))
	}
	remaining, err := database.KilledChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	survivors := make([]string, 0, len(remaining))
	for _, killed := range remaining {
		survivors = append(survivors, killed.ID)
	}
	wantSurvivors := []string{"lineage-root", "live-cc", "live-cx"}
	if !reflect.DeepEqual(survivors, wantSurvivors) {
		t.Fatalf("KilledChats() after prune = %v, want %v", survivors, wantSurvivors)
	}

	counts, err = database.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.OrphanedKills != 0 {
		t.Fatalf("Counts().OrphanedKills after prune = %d, want 0", counts.OrphanedKills)
	}
	deleted, err = database.DeleteOrphanedKills(ctx)
	if err != nil || deleted != 0 {
		t.Fatalf("second DeleteOrphanedKills() = %d, %v; want 0, nil", deleted, err)
	}
}

func TestKilledWriteHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		t.Skip("helper process only")
	}
	if os.Getenv(paths.EnvCacheDB) == "" {
		t.Fatal("helper process has no PFM_CACHE_DB jail")
	}

	store := openFileTestStore(t)
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	}()
	ready := os.Getenv(helperReadyEnv)
	gate := os.Getenv(helperGateEnv)
	if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
		t.Fatalf("write ready marker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := waitForPaths(ctx, gate); err != nil {
		t.Fatalf("wait for gate: %v", err)
	}

	prefix := os.Getenv(helperPrefixEnv)
	engine := pfmengine.Claude
	if prefix == "b" {
		engine = pfmengine.Codex
	}
	for index := 0; index < helperWriteCount; index++ {
		baseline := int64(index)
		if err := store.Kill(ctx, Killed{
			ID:              fmt.Sprintf("%s-%03d", prefix, index),
			Engine:          engine,
			KilledAt:        int64(index + 1),
			BaselinePrompts: &baseline,
		}); err != nil {
			t.Fatalf("Kill(%s, %d) error = %v", prefix, index, err)
		}
	}
}

func waitForPaths(ctx context.Context, filePaths ...string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		allPresent := true
		for _, filePath := range filePaths {
			if _, err := os.Stat(filePath); err != nil {
				if !os.IsNotExist(err) {
					return err
				}
				allPresent = false
				break
			}
		}
		if allPresent {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
