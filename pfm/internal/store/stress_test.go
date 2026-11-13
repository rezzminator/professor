package store

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
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

	timeout := 3 * time.Minute
	if os.Getenv("PFM_STRESS_STRICT") == "1" {
		timeout = 40 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := waitForPaths(ctx, gate); err != nil {
		t.Fatalf("wait for store stress gate: %v", err)
	}

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
