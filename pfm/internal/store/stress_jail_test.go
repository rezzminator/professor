//go:build jail

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

	"github.com/rezzminator/professor/pfm/internal/testwait"
)

func TestStoreStress(t *testing.T) {
	if os.Getenv(storeStressEnv) != "1" {
		t.Skip("set PFM_STRESS=1 to run the store stress battery")
	}

	dbPath := setStoreTestJail(t)
	stressKilledContention(t, dbPath)

	store := openFileTestStore(t)
	stressTranscriptVolume(t, store)
	stressBigValues(t, store)
	if err := store.Close(); err != nil {
		t.Fatalf("close populated stress database: %v", err)
	}

	stressReopen(t, dbPath)
}

func stressKilledContention(t *testing.T, dbPath string) {
	t.Helper()

	initial := openFileTestStore(t)
	if err := initial.Close(); err != nil {
		t.Fatalf("close initial stress store: %v", err)
	}

	root := filepath.Dir(dbPath)
	gate := filepath.Join(root, "stress-go")
	ctx := testwait.Context(t)

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

	waitForPaths(t, readyPaths...)
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
	store := openFileTestStore(t)
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
		"contention: %d processes x %d upserts = %d rows; busy warnings=%d",
		storeStressProcesses,
		storeStressWrites,
		count,
		warningCount,
	)
}

func stressTranscriptVolume(t *testing.T, store *Store) {
	t.Helper()

	stressTranscriptPass(t, store, false)
	stressTranscriptPass(t, store, true)

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
	t.Logf("bulk: 50k insert and update passes, %d rows", count)
}

func stressBigValues(t *testing.T, store *Store) {
	t.Helper()

	value := strings.Repeat("0123456789abcdef", 65536)
	if len(value) != 1<<20 {
		t.Fatalf("stress prompt size = %d, want %d", len(value), 1<<20)
	}

	ctx := context.Background()
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
		"big values: %d x 1 MiB last_prompt round trips",
		storeStressBigRows,
	)
}

func stressReopen(t *testing.T, dbPath string) {
	t.Helper()

	before := fileSize(dbPath + "-wal")
	maxWAL := before
	for cycle := 0; cycle < 100; cycle++ {
		store := openFileTestStore(t)
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
		"reopen: 100 Open/migrate/Close cycles; WAL bytes before=%d max=%d after=%d",
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
