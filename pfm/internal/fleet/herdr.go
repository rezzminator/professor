package fleet

import (
	"context"
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// NativeSeats reads the same durable native seats for every fleet surface.
// Non-live rows retain their failure without probing an address they no longer
// own; live probe failures remain rows as well as scan errors.
func NativeSeats(ctx context.Context, values paths.Values, readOnly bool) ([]gather.HerdrSeat, []string, error) {
	durable, err := fleetdb.ReadSeats(ctx, values)
	if err != nil {
		return nil, nil, fmt.Errorf("read durable seats: %w", err)
	}
	seats := gather.ProbeHerdrSeats(ctx, values, durable, readOnly)
	for index := range durable {
		seat := &durable[index]
		if seat.Backend == "herdr" && seat.State != "live" {
			seats = append(seats, gather.HerdrSeat{
				Seat: *seat,
				Err:  fmt.Errorf("HERDR %s %s: %s", strings.ToUpper(seat.State), seat.HerdrSocket, seat.LostNote),
			})
		}
	}
	var failures []string
	for index := range seats {
		if seats[index].Seat.State == "live" && seats[index].Err != nil {
			failures = append(failures, seats[index].Err.Error())
		}
	}
	return seats, failures, nil
}

// EnrichNative adds only native identities omitted by the capped default load.
// Kills and OpenCode sessions are already complete; cached totals remain valid.
func EnrichNative(ctx context.Context, database *store.Store, data Data, native []gather.HerdrSeat) (Data, error) {
	if data.CachedCounts == nil || len(native) == 0 {
		return data, nil
	}
	transcripts := make(map[string]struct{}, len(data.Transcripts))
	for index := range data.Transcripts {
		transcripts[data.Transcripts[index].UUID] = struct{}{}
	}
	rollouts := make(map[string]struct{}, len(data.Rollouts))
	for index := range data.Rollouts {
		rollouts[data.Rollouts[index].ID] = struct{}{}
	}
	for index := range native {
		seat := &native[index].Seat
		if seat.SessionID == "" {
			continue
		}
		switch pfmengine.ID(seat.Engine) {
		case pfmengine.Claude:
			if _, found := transcripts[seat.SessionID]; found {
				continue
			}
			transcript, found, err := database.Transcript(ctx, seat.SessionID)
			if err != nil {
				return Data{}, fmt.Errorf("native seat %s transcript: %w", seat.SeatID, err)
			}
			transcripts[seat.SessionID] = struct{}{}
			if found {
				data.Transcripts = append(data.Transcripts, transcript)
			}
		case pfmengine.Codex:
			if _, found := rollouts[seat.SessionID]; found {
				continue
			}
			rollout, found, err := database.Rollout(ctx, seat.SessionID)
			if err != nil {
				return Data{}, fmt.Errorf("native seat %s rollout: %w", seat.SeatID, err)
			}
			rollouts[seat.SessionID] = struct{}{}
			if found {
				data.Rollouts = append(data.Rollouts, rollout)
			}
		}
	}
	return data, nil
}

// composeNativeFleet is the shared native-read policy after a scan has loaded
// indexed data and, for a full scan, gathered its tmux snapshot. Native database
// failure must never discard that healthy snapshot.
func composeNativeFleet(
	ctx context.Context,
	database *store.Store,
	environment Env,
	view compose.View,
	data Data,
	live gather.Snapshot,
	readOnly bool,
) (compose.Output, []string, error) {
	native, failures, err := NativeSeats(ctx, environment.Paths, readOnly)
	if err != nil {
		failures = append(failures, err.Error())
	}
	data, err = EnrichNative(ctx, database, data, native)
	if err != nil {
		return compose.Output{}, nil, err
	}
	return ComposeFleet(environment, view, data, live, native...), failures, nil
}
