package kill

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/store"
)

func TestFinisherKeepsAnUnkillThatLandedDuringTheRefresh(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	ctx := context.Background()
	id := "88888888-8888-4888-8888-888888888888"
	transcriptPath := filepath.Join(jail.claudeRoot, id+".jsonl")
	if err := database.UpsertTranscript(
		ctx,
		store.Transcript{UUID: id, Path: transcriptPath, PromptCount: 1},
	); err != nil {
		t.Fatal(err)
	}
	if err := database.Kill(ctx, store.Killed{ID: id, Engine: pfmengine.Claude, KilledAt: 100}); err != nil {
		t.Fatal(err)
	}
	runPostExitFinisher(t, jail, database, id, transcriptPath, refreshFunc(func(ctx context.Context) error {
		_, err := database.Unkill(ctx, id)
		return err
	}))
	if _, found, err := database.Killed(ctx, id); err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatal("finisher re-killed a chat after unkill landed during its refresh")
	}
}

func TestFinisherReassertsAStandingKillKeepingItsTime(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	ctx := context.Background()
	id := "77777777-7777-4777-8777-777777777777"
	transcriptPath := filepath.Join(jail.claudeRoot, id+".jsonl")
	if err := database.UpsertTranscript(
		ctx,
		store.Transcript{UUID: id, Path: transcriptPath, PromptCount: 1},
	); err != nil {
		t.Fatal(err)
	}
	baseline := int64(1)
	if err := database.Kill(ctx, store.Killed{
		ID: id, Engine: pfmengine.Claude, KilledAt: 100, BaselinePrompts: &baseline,
	}); err != nil {
		t.Fatal(err)
	}
	runPostExitFinisher(t, jail, database, id, transcriptPath, refreshFunc(func(context.Context) error { return nil }))
	killed, found, err := database.Killed(ctx, id)
	if err != nil || !found || killed.KilledAt != 100 || killed.BaselinePrompts != nil {
		t.Fatalf("standing kill = %#v, found=%v, err=%v; want original time and permanent kill", killed, found, err)
	}
}

func TestFinisherWritesNoKillWhenNoneStood(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	ctx := context.Background()
	id := "66666666-6666-4666-8666-666666666666"
	transcriptPath := filepath.Join(jail.claudeRoot, id+".jsonl")
	if err := database.UpsertTranscript(
		ctx,
		store.Transcript{UUID: id, Path: transcriptPath, PromptCount: 1},
	); err != nil {
		t.Fatal(err)
	}
	runPostExitFinisher(t, jail, database, id, transcriptPath, refreshFunc(func(context.Context) error { return nil }))
	if _, found, err := database.Killed(ctx, id); err != nil || found {
		t.Fatalf("kill without an operator row: found=%v err=%v", found, err)
	}
}

func runPostExitFinisher(
	t *testing.T, jail killJail, database *store.Store, id, transcriptPath string, refresher refreshFunc,
) {
	t.Helper()
	finisher, err := NewFinisher(database, Dependencies{
		Tmux: &fakeTmux{existsFor: 1}, Refresher: refresher,
		Delay: time.Millisecond, PollEvery: time.Millisecond, PollAttempts: 3,
		Now: func() time.Time { return time.Unix(200, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := finisher.Run(context.Background(), ExitArgs{
		Engine: pfmengine.Claude, ID: id, DataPath: transcriptPath,
		SocketPath: filepath.Join(jail.tmuxDir, "cc-600-1-1"),
		SocketName: "cc-600-1-1", PaneID: "%9",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestManagerUnkillReportsWhetherItCleared(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	ctx := context.Background()
	manager, err := New(database, Dependencies{
		ProcFS: &fakeProc{}, Tmux: &fakeTmux{}, Spawner: &captureSpawner{},
		Now: func() time.Time { return time.Unix(50, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	plainID := "55555555-5555-4555-8555-555555555555"
	if err := database.UpsertTranscript(
		ctx,
		store.Transcript{UUID: plainID, Path: filepath.Join(jail.claudeRoot, plainID+".jsonl")},
	); err != nil {
		t.Fatal(err)
	}
	if cleared, err := manager.Unkill(ctx, plainID); err != nil || cleared {
		t.Fatalf("plain no-op = %v, %v; want false, nil", cleared, err)
	}
	if err := database.Kill(ctx, store.Killed{ID: plainID, Engine: pfmengine.Claude, KilledAt: 10}); err != nil {
		t.Fatal(err)
	}
	if cleared, err := manager.Unkill(ctx, plainID); err != nil || !cleared {
		t.Fatalf("plain kill = %v, %v; want true, nil", cleared, err)
	}

	rootID := "44444444-4444-4444-8444-444444444444"
	childID := "33333333-3333-4333-8333-333333333333"
	for _, rollout := range []store.Rollout{
		{ID: rootID, SessionID: rootID, UserThread: true, Path: filepath.Join(jail.codexHome, "root.jsonl")},
		{ID: childID, SessionID: rootID, UserThread: true, Path: filepath.Join(jail.codexHome, "child.jsonl")},
	} {
		if err := database.UpsertRollout(ctx, rollout); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{rootID, childID} {
		if err := database.Kill(ctx, store.Killed{ID: id, Engine: pfmengine.Codex, KilledAt: 20}); err != nil {
			t.Fatal(err)
		}
	}
	if cleared, err := manager.Unkill(ctx, rootID); err != nil || !cleared {
		t.Fatalf("lineage kill = %v, %v; want true, nil", cleared, err)
	}
	if killed, err := database.KilledChats(ctx); err != nil || len(killed) != 0 {
		t.Fatalf("lineage kills remain = %#v, %v", killed, err)
	}
	if cleared, err := manager.Unkill(ctx, childID); err != nil || cleared {
		t.Fatalf("lineage no-op = %v, %v; want false, nil", cleared, err)
	}
}
