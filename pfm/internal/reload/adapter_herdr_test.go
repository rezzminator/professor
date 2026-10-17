package reload

import (
	"context"
	"path/filepath"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestNativeReloadAdapterTarget(t *testing.T) {
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	socket := filepath.Join(t.TempDir(), "arbitrary.sock")
	for _, pane := range []string{"w6:p3", "w6:p4"} {
		if err := db.PutSeat(
			ctx,
			fleetdb.Seat{
				SeatID:      pane,
				Backend:     "herdr",
				Engine:      string(pfmengine.Codex),
				State:       "live",
				HerdrSocket: socket,
				HerdrPane:   pane,
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name, sock, pane string
		bad              bool
	}{{"selected", socket, "w6:p3", false}, {"ambiguous", socket, "", true}, {"missing-pane", socket, "w6:p7", true}} {
		t.Run(test.name, func(t *testing.T) {
			seat, native, err := NativeTarget(ctx, values, test.sock, test.pane, &paths.MapEnv{})
			if !native || (err != nil) != test.bad ||
				(!test.bad && (seat == nil || seat.HerdrPane != test.pane || seat.Engine != string(pfmengine.Codex))) {
				t.Fatalf("seat=%+v native=%v err=%v", seat, native, err)
			}
		})
	}
	seat, err := SeatForReload(ctx, values, socket, "w6:p3")
	if err != nil || seat == nil || seat.HerdrPane != "w6:p3" {
		t.Fatalf("seat=%+v err=%v", seat, err)
	}
}

func TestNativeReloadAdapterEngine(t *testing.T) {
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	socket := filepath.Join(t.TempDir(), "arbitrary.sock")
	if err := db.PutSeat(
		ctx,
		fleetdb.Seat{
			SeatID:      "engine-adapter",
			Backend:     "herdr",
			Engine:      string(pfmengine.Codex),
			State:       "live",
			HerdrSocket: socket,
			HerdrPane:   "w6:p3",
		},
	); err != nil {
		t.Fatal(err)
	}
	got, err := EngineForTarget(ctx, values, socket, "w6:p3")
	if err != nil || got != pfmengine.Codex {
		t.Fatalf("engine=%q err=%v", got, err)
	}
}
