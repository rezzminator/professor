package kill

import (
	"context"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestManagerKillRecordsATransition: Kill is a state door — one comp=state
// record from `live` to `killed` naming the request shape as cause; a
// refused kill (no target) ends `failed` at ERROR; KillCleared on an
// unindexed id is `skipped`.
func TestManagerKillRecordsATransition(t *testing.T) {
	ctx, recorder := obs.Test(t)
	jail := newKillJail(t)
	database := jail.open(t)
	manager, err := New(
		database,
		Dependencies{Spawner: &captureSpawner{}, Now: func() time.Time { return time.Unix(600, 0) }},
	)
	if err != nil {
		t.Fatal(err)
	}
	const id = "20202020-2020-4020-8020-202020202020"
	if _, err := manager.Kill(
		ctx,
		Request{ID: id, Engine: pfmengine.Codex, Exit: true, SocketName: "probe-codex-self", PaneID: "%7"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Kill(ctx, Request{}); err == nil {
		t.Fatal("a kill without a target was accepted")
	}
	if _, found, err := manager.KillCleared(ctx, "30303030-3030-4030-8030-303030303030"); err != nil || found {
		t.Fatalf("KillCleared on an unindexed id = found %t, %v", found, err)
	}
	var kill []obs.Record
	for _, record := range recorder.Records() {
		kind, _ := record.Field("kind")
		if record.Message == "state.transition" && kind == "kill" {
			kill = append(kill, record)
		}
	}
	if len(kill) != 3 {
		t.Fatalf("kill transitions = %d, want 3: %s", len(kill), recorder.Raw())
	}
	for key, want := range map[string]any{obs.FieldComp: "state", "prior": "live", "next": "killed", "cause": "id --exit"} {
		if got, _ := kill[0].Field(key); got != want {
			t.Fatalf("%s = %v, want %v", key, got, want)
		}
	}
	if _, found := kill[0].Field(obs.FieldDur); !found {
		t.Fatalf("no dur_ms: %v", kill[0].Fields)
	}
	if next, _ := kill[1].Field("next"); next != "failed" || kill[1].Level != "ERROR" {
		t.Fatalf("refused kill = %v at %s, want failed at ERROR", next, kill[1].Level)
	}
	if next, _ := kill[2].Field("next"); next != "skipped" {
		t.Fatalf("KillCleared on an unindexed id = %v, want skipped", next)
	}
	_ = context.Background
}
