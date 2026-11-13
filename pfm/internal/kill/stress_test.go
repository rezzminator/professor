package kill

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/store"
)

const (
	killStressHelperEnv = "PFM_KILL_STRESS_HELPER"
	killStressIndexEnv  = "PFM_KILL_STRESS_INDEX"
	killStressReadyEnv  = "PFM_KILL_STRESS_READY"
	killStressGateEnv   = "PFM_KILL_STRESS_GATE"
	killStressHomeEnv   = "PFM_KILL_STRESS_HOME"
	killStressProcesses = 20
	killStressIDs       = 5
)

func TestKillStressProcessHelper(t *testing.T) {
	if os.Getenv(killStressHelperEnv) != "1" {
		t.Skip("kill stress fixture runs only as a helper process")
	}
	index := 0
	if _, err := fmt.Sscanf(os.Getenv(killStressIndexEnv), "%d", &index); err != nil {
		t.Fatal(err)
	}
	// The helper is a fresh test process: testjail.Run gave it a PFM_HOME of its
	// own, and the shared state store lives under PFM_HOME, so it takes the
	// parent's home back to write the rows the parent reads.
	t.Setenv("PFM_HOME", os.Getenv(killStressHomeEnv))
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
		if _, err := manager.Unkill(ctx, drop); err != nil {
			t.Fatal(err)
		}
	}
}
