package store

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

const (
	storeStressEnv         = "PFM_STRESS"
	storeStressHelperEnv   = "PFM_STORE_STRESS_HELPER"
	storeStressPrefixEnv   = "PFM_STORE_STRESS_PREFIX"
	storeStressReadyEnv    = "PFM_STORE_STRESS_READY"
	storeStressGateEnv     = "PFM_STORE_STRESS_GATE"
	storeStressCountEnv    = "PFM_STORE_STRESS_COUNT"
	storeStressProcesses   = 8
	storeStressWrites      = 2000
	storeStressTranscripts = 50000
	storeStressBigRows     = 4
)

func TestStoreStressKilledHelper(t *testing.T) {
	if os.Getenv(storeStressHelperEnv) != "1" {
		t.Skip("store stress helper process only")
	}
	if os.Getenv(paths.EnvCacheDB) == "" {
		t.Fatal("store stress helper has no PFM_CACHE_DB jail")
	}

	count, err := strconv.Atoi(os.Getenv(storeStressCountEnv))
	if err != nil || count < 1 {
		t.Fatalf("invalid store stress write count %q", os.Getenv(storeStressCountEnv))
	}
	store := openFileTestStore(t)
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	}()

	ready := os.Getenv(storeStressReadyEnv)
	gate := os.Getenv(storeStressGateEnv)
	if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
		t.Fatalf("write store stress ready marker: %v", err)
	}

	ctx := testwait.Context(t)
	waitForPaths(t, gate)

	prefix := os.Getenv(storeStressPrefixEnv)
	engine := pfmengine.Claude
	if strings.HasSuffix(prefix, "1") ||
		strings.HasSuffix(prefix, "3") ||
		strings.HasSuffix(prefix, "5") ||
		strings.HasSuffix(prefix, "7") {
		engine = pfmengine.Codex
	}
	for index := 0; index < count; index++ {
		baseline := int64(index)
		if err := store.Kill(ctx, Killed{
			ID:              fmt.Sprintf("%s-%04d", prefix, index),
			Engine:          engine,
			KilledAt:        int64(index + 1),
			BaselinePrompts: &baseline,
		}); err != nil {
			t.Fatalf("stress Kill(%s, %d): %v", prefix, index, err)
		}
	}
}

// stressTranscriptPass writes (or updates) the 50k stress transcripts in
// BatchSize transactions.
func stressTranscriptPass(tb testing.TB, store *Store, update bool) {
	tb.Helper()

	ctx := context.Background()
	for base := 0; base < storeStressTranscripts; base += BatchSize {
		count := min(BatchSize, storeStressTranscripts-base)
		err := store.Batch(ctx, count, func(tx *ImmediateTx, start, end int) error {
			for offset := start; offset < end; offset++ {
				index := base + offset
				promptCount := int64(1)
				lastPrompt := "initial"
				if update {
					promptCount = 2
					lastPrompt = "updated"
				}
				if err := tx.UpsertTranscript(ctx, Transcript{
					UUID:         fmt.Sprintf("stress-%05d", index),
					Path:         fmt.Sprintf("/stress/%05d.jsonl", index),
					Size:         int64(index + 100),
					MTimeNS:      int64(index + 200),
					ParsedOffset: int64(index + 90),
					CWD:          "/stress",
					FirstPrompt:  "first",
					LastPrompt:   lastPrompt,
					PromptCount:  promptCount,
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			tb.Fatalf("stress transcript batch at %d: %v", base, err)
		}
	}
}

// BenchmarkStoreStressTranscriptPass is the 50k-transcript insert and update
// pass TestStoreStress asserts the rows of, on an in-memory store.
func BenchmarkStoreStressTranscriptPass(b *testing.B) {
	for b.Loop() {
		store, err := OpenMemory()
		if err != nil {
			b.Fatalf("OpenMemory() error = %v", err)
		}
		stressTranscriptPass(b, store, false)
		stressTranscriptPass(b, store, true)
		if err := store.Close(); err != nil {
			b.Fatalf("close store: %v", err)
		}
	}
}
