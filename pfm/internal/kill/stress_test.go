package kill

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/store"
)

const (
	killStressHelperEnv = "PFM_KILL_STRESS_HELPER"
	killStressIndexEnv  = "PFM_KILL_STRESS_INDEX"
	killStressReadyEnv  = "PFM_KILL_STRESS_READY"
	killStressGateEnv   = "PFM_KILL_STRESS_GATE"
	killStressProcesses = 20
	killStressIDs       = 5
)

func TestStressTwentyKillUnkillProcesses(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := database.Batch(
		ctx,
		killStressProcesses*killStressIDs*2,
		func(tx *store.ImmediateTx, start, end int) error {
			for index := start; index < end; index++ {
				process := index / (killStressIDs * 2)
				offset := (index / 2) % killStressIDs
				kind := "keep"
				if index%2 == 1 {
					kind = "drop"
				}
				id := fmt.Sprintf("%s-%02d-%02d", kind, process, offset)
				if err := tx.UpsertTranscript(ctx, store.Transcript{
					UUID: id,
					Path: "/jailed/" + id + ".jsonl",
				}); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	gate := filepath.Join(jail.root, "kill-stress-go")
	processes := make([]*killStressProcess, 0, killStressProcesses)
	readyPaths := make([]string, 0, killStressProcesses)
	for index := 0; index < killStressProcesses; index++ {
		ready := filepath.Join(jail.root, fmt.Sprintf("ready-%02d", index))
		command := exec.CommandContext(
			ctx,
			os.Args[0],
			"-test.run=^TestKillStressProcessHelper$",
		)
		command.Env = append(
			os.Environ(),
			killStressHelperEnv+"=1",
			fmt.Sprintf("%s=%d", killStressIndexEnv, index),
			killStressReadyEnv+"="+ready,
			killStressGateEnv+"="+gate,
		)
		process := &killStressProcess{command: command}
		command.Stdout = &process.output
		command.Stderr = &process.output
		if err := command.Start(); err != nil {
			t.Fatalf("start helper %d: %v", index, err)
		}
		processes = append(processes, process)
		readyPaths = append(readyPaths, ready)
	}
	t.Cleanup(func() {
		for _, process := range processes {
			if process.command.Process != nil {
				_ = process.command.Process.Kill()
			}
		}
	})
	if err := waitKillStressPaths(ctx, readyPaths); err != nil {
		t.Fatalf("wait for helpers: %v\n%s", err, killStressOutputs(processes))
	}
	started := time.Now()
	if err := os.WriteFile(gate, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	warnings := 0
	for index, process := range processes {
		if err := process.command.Wait(); err != nil {
			t.Fatalf("helper %d: %v\n%s", index, err, process.output.String())
		}
		warnings += strings.Count(process.output.String(), "WARNING:")
	}
	elapsed := time.Since(started)

	database = jail.open(t)
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	killed, err := database.KilledChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := killStressProcesses * killStressIDs
	if len(killed) != want {
		t.Fatalf(
			"killed count=%d, want=%d; warnings=%d",
			len(killed),
			want,
			warnings,
		)
	}
	for _, row := range killed {
		if !strings.HasPrefix(row.ID, "keep-") {
			t.Fatalf("unkill lost for %#v", row)
		}
	}
	if warnings != 0 {
		t.Fatalf(
			"contention dropped accepted writes: warnings=%d, want 0",
			warnings,
		)
	}
	t.Logf(
		"STRESS kill/unkill processes=%d operations=%d final=%d warnings=%d elapsed=%s",
		killStressProcesses,
		killStressProcesses*killStressIDs*3,
		len(killed),
		warnings,
		elapsed,
	)
}

func TestKillStressProcessHelper(t *testing.T) {
	if os.Getenv(killStressHelperEnv) != "1" {
		return
	}
	index := 0
	if _, err := fmt.Sscanf(os.Getenv(killStressIndexEnv), "%d", &index); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open(store.WithWarningWriter(os.Stdout))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	manager, err := New(database, Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		os.Getenv(killStressReadyEnv),
		[]byte("ready"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		if _, err := os.Stat(os.Getenv(killStressGateEnv)); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	for offset := 0; offset < killStressIDs; offset++ {
		keep := fmt.Sprintf("keep-%02d-%02d", index, offset)
		drop := fmt.Sprintf("drop-%02d-%02d", index, offset)
		if _, err := manager.Kill(ctx, Request{ID: keep}); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Kill(ctx, Request{ID: drop}); err != nil {
			t.Fatal(err)
		}
		if err := manager.Unkill(ctx, drop); err != nil {
			t.Fatal(err)
		}
	}
}

type killStressProcess struct {
	command *exec.Cmd
	output  bytes.Buffer
}

func waitKillStressPaths(ctx context.Context, paths []string) error {
	pending := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		pending[path] = struct{}{}
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for len(pending) != 0 {
		for path := range pending {
			if _, err := os.Stat(path); err == nil {
				delete(pending, path)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return nil
}

func killStressOutputs(processes []*killStressProcess) string {
	var output strings.Builder
	for index, process := range processes {
		fmt.Fprintf(&output, "\nhelper %d:\n%s", index, process.output.String())
	}
	return output.String()
}
