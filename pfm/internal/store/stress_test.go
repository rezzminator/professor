package store

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func TestStoreStress(t *testing.T) {
	if os.Getenv(storeStressEnv) != "1" {
		t.Skip("set PFM_STRESS=1 to run the store stress battery")
	}

	dbPath := setStoreTestJail(t)
	stressKilledContention(t, dbPath)

	store := openTestStore(t)
	stressTranscriptVolume(t, store)
	stressBigValues(t, store)
	if err := store.Close(); err != nil {
		t.Fatalf("close populated stress database: %v", err)
	}

	stressReopen(t, dbPath)
}

func stressKilledContention(t *testing.T, dbPath string) {
	t.Helper()

	initial := openTestStore(t)
	if err := initial.Close(); err != nil {
		t.Fatalf("close initial stress store: %v", err)
	}

	root := filepath.Dir(dbPath)
	gate := filepath.Join(root, "stress-go")
	timeout := 3 * time.Minute
	if os.Getenv("PFM_STRESS_STRICT") == "1" {
		timeout = 45 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	processes := make([]*storeStressProcess, 0, storeStressProcesses)
	readyPaths := make([]string, 0, storeStressProcesses)
	for index := 0; index < storeStressProcesses; index++ {
		prefix := fmt.Sprintf("p%d", index)
		ready := filepath.Join(root, "stress-ready-"+prefix)
		process := newStoreStressProcess(ctx, prefix, ready, gate)
		if err := process.cmd.Start(); err != nil {
			t.Fatalf("start stress helper %s: %v", prefix, err)
		}
		t.Cleanup(func() {
			if process.cmd.Process != nil {
				_ = process.cmd.Process.Kill()
			}
		})
		processes = append(processes, process)
		readyPaths = append(readyPaths, ready)
	}

	if err := waitForPaths(ctx, readyPaths...); err != nil {
		t.Fatalf("wait for stress helpers: %v\n%s", err, stressProcessOutput(processes))
	}
	started := time.Now()
	if err := os.WriteFile(gate, []byte("go"), 0o600); err != nil {
		t.Fatalf("release stress helper gate: %v", err)
	}

	warningCount := 0
	for _, process := range processes {
		if err := process.cmd.Wait(); err != nil {
			t.Fatalf("stress helper %s: %v\n%s", process.prefix, err, process.output.String())
		}
		warningCount += strings.Count(process.output.String(), "WARNING:")
	}
	elapsed := time.Since(started)

	store := openTestStore(t)
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	}()
	records, err := store.Shared().KilledRecords(context.Background())
	if err != nil {
		t.Fatalf("count shared stress killed rows: %v", err)
	}
	count := len(records)
	want := storeStressProcesses * storeStressWrites
	if count != want {
		t.Fatalf("stress killed row count = %d, want %d", count, want)
	}
	t.Logf(
		"contention: %d processes x %d upserts = %d rows in %s; busy warnings=%d",
		storeStressProcesses,
		storeStressWrites,
		count,
		elapsed.Round(time.Millisecond),
		warningCount,
	)
}

func stressTranscriptVolume(t *testing.T, store *Store) {
	t.Helper()

	strict := os.Getenv("PFM_STRESS_STRICT") == "1"
	insertElapsed, insertMax := stressTranscriptPass(t, store, false)
	updateElapsed, updateMax := stressTranscriptPass(t, store, true)
	passLimit := 3 * time.Minute
	batchLimit := 10 * time.Second
	if strict {
		passLimit = 30 * time.Second
		batchLimit = time.Second
	}
	if insertElapsed > passLimit || updateElapsed > passLimit {
		t.Fatalf(
			"50k transcript pass too slow: insert=%s update=%s limit=%s strict=%t",
			insertElapsed,
			updateElapsed,
			passLimit,
			strict,
		)
	}
	if insertMax > batchLimit || updateMax > batchLimit {
		t.Fatalf(
			"batch held writer lock too long: insert max=%s update max=%s limit=%s strict=%t",
			insertMax,
			updateMax,
			batchLimit,
			strict,
		)
	}

	var count int
	var promptCount int64
	if err := store.db.QueryRow(
		"SELECT count(*), sum(prompt_count) FROM transcripts",
	).Scan(&count, &promptCount); err != nil {
		t.Fatalf("summarize stress transcripts: %v", err)
	}
	if count != storeStressTranscripts || promptCount != storeStressTranscripts*2 {
		t.Fatalf(
			"stress transcript summary = count %d, prompt sum %d; want %d, %d",
			count,
			promptCount,
			storeStressTranscripts,
			storeStressTranscripts*2,
		)
	}
	t.Logf(
		"bulk: 50k insert=%s (max 500-row txn %s), update=%s (max %s), strict=%t",
		insertElapsed.Round(time.Millisecond),
		insertMax.Round(time.Millisecond),
		updateElapsed.Round(time.Millisecond),
		updateMax.Round(time.Millisecond),
		strict,
	)
}

func stressTranscriptPass(
	t *testing.T,
	store *Store,
	update bool,
) (elapsed, maxBatch time.Duration) {
	t.Helper()

	ctx := context.Background()
	started := time.Now()
	for base := 0; base < storeStressTranscripts; base += BatchSize {
		count := min(BatchSize, storeStressTranscripts-base)
		batchStarted := time.Now()
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
			t.Fatalf("stress transcript batch at %d: %v", base, err)
		}
		maxBatch = max(maxBatch, time.Since(batchStarted))
	}
	return time.Since(started), maxBatch
}

func stressBigValues(t *testing.T, store *Store) {
	t.Helper()

	value := strings.Repeat("0123456789abcdef", 65536)
	if len(value) != 1<<20 {
		t.Fatalf("stress prompt size = %d, want %d", len(value), 1<<20)
	}

	ctx := context.Background()
	started := time.Now()
	for index := 0; index < storeStressBigRows; index++ {
		if err := store.UpsertTranscript(ctx, Transcript{
			UUID:        fmt.Sprintf("big-%d", index),
			Path:        fmt.Sprintf("/stress/big-%d.jsonl", index),
			LastPrompt:  value,
			PromptCount: 1,
		}); err != nil {
			t.Fatalf("upsert 1 MiB prompt %d: %v", index, err)
		}
	}
	for index := 0; index < storeStressBigRows; index++ {
		transcript, found, err := store.Transcript(ctx, fmt.Sprintf("big-%d", index))
		if err != nil {
			t.Fatalf("read 1 MiB prompt %d: %v", index, err)
		}
		if !found || transcript.LastPrompt != value {
			t.Fatalf("1 MiB prompt %d failed round trip", index)
		}
	}
	t.Logf(
		"big values: %d x 1 MiB last_prompt round trips in %s",
		storeStressBigRows,
		time.Since(started).Round(time.Millisecond),
	)
}

func stressReopen(t *testing.T, dbPath string) {
	t.Helper()

	before := fileSize(dbPath + "-wal")
	maxWAL := before
	started := time.Now()
	for cycle := 0; cycle < 100; cycle++ {
		store := openTestStore(t)
		version, err := store.UserVersion(context.Background())
		if err != nil {
			_ = store.Close()
			t.Fatalf("reopen cycle %d user_version: %v", cycle, err)
		}
		if version != SchemaVersion {
			_ = store.Close()
			t.Fatalf("reopen cycle %d user_version = %d, want %d", cycle, version, SchemaVersion)
		}
		maxWAL = max(maxWAL, fileSize(dbPath+"-wal"))
		if err := store.Close(); err != nil {
			t.Fatalf("reopen cycle %d close: %v", cycle, err)
		}
		maxWAL = max(maxWAL, fileSize(dbPath+"-wal"))
	}
	elapsed := time.Since(started)
	after := fileSize(dbPath + "-wal")
	if maxWAL > before+(4<<20) {
		t.Fatalf(
			"WAL grew unexpectedly across read-only reopen storm: before=%d max=%d after=%d",
			before,
			maxWAL,
			after,
		)
	}
	t.Logf(
		"reopen: 100 Open/migrate/Close cycles in %s; WAL bytes before=%d max=%d after=%d",
		elapsed.Round(time.Millisecond),
		before,
		maxWAL,
		after,
	)
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

type storeStressProcess struct {
	cmd    *exec.Cmd
	prefix string
	output bytes.Buffer
}

func newStoreStressProcess(
	ctx context.Context,
	prefix string,
	ready string,
	gate string,
) *storeStressProcess {
	process := &storeStressProcess{prefix: prefix}
	process.cmd = exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestStoreStressKilledHelper$",
	)
	process.cmd.Env = append(
		os.Environ(),
		storeStressHelperEnv+"=1",
		storeStressPrefixEnv+"="+prefix,
		storeStressReadyEnv+"="+ready,
		storeStressGateEnv+"="+gate,
		storeStressCountEnv+"="+strconv.Itoa(storeStressWrites),
	)
	process.cmd.Stdout = &process.output
	process.cmd.Stderr = &process.output
	return process
}

func stressProcessOutput(processes []*storeStressProcess) string {
	var output strings.Builder
	for _, process := range processes {
		fmt.Fprintf(&output, "%s:\n%s\n", process.prefix, process.output.String())
	}
	return output.String()
}

func TestStoreStressKilledHelper(t *testing.T) {
	if os.Getenv(storeStressHelperEnv) != "1" {
		t.Skip("store stress helper process only")
	}
	if os.Getenv(paths.EnvDB) == "" {
		t.Fatal("store stress helper has no PFM_DB jail")
	}

	count, err := strconv.Atoi(os.Getenv(storeStressCountEnv))
	if err != nil || count < 1 {
		t.Fatalf("invalid store stress write count %q", os.Getenv(storeStressCountEnv))
	}
	store := openTestStore(t)
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
