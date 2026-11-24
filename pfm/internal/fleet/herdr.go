package fleet

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// liveSeat is the state of a durable seat whose engine runs.
const liveSeat = "live"

// NativeSeats reads the same durable native seats for every fleet surface.
// Non-live rows retain their failure without probing an address they no longer
// own; a positively dead seat arrives non-live from the probe and is such a row.
// Live probe failures, unknown ownership included, remain rows and scan errors.
func NativeSeats(
	ctx context.Context,
	values paths.Values,
	readOnly bool,
) ([]gather.HerdrSeat, []string, error) {
	durable, err := fleetdb.ReadSeats(ctx, values)
	if err != nil {
		return nil, nil, fmt.Errorf("read durable seats: %w", err)
	}
	seats := gather.ProbeHerdrSeats(ctx, values, durable, readOnly)
	for index := range durable {
		seat := &durable[index]
		if seat.Backend == "herdr" && seat.State != liveSeat {
			seats = append(seats, gather.HerdrSeat{
				Seat: *seat,
				Err:  fmt.Errorf("HERDR %s %s: %s", strings.ToUpper(seat.State), seat.HerdrSocket, seat.LostNote),
			})
		}
	}
	var failures []string
	for index := range seats {
		if seats[index].Seat.State == liveSeat && seats[index].Err != nil {
			failures = append(failures, seats[index].Err.Error())
		}
	}
	return seats, failures, nil
}

// ComposeNative composes view's rows from loaded data, the live snapshot and
// the native seats: the one door every scan and picker pass composes native
// seats through. It enriches the seats (enrichNative) and composes; over
// capped data (LoadDefaultData) whose live rows claim a chat, it re-reads the
// capped candidates and totals around those claims (refillDefault) and
// composes again, so a capped frame lists the rows and counts a full frame
// does. Every read failure is joined in the error beside an output still
// composed: a failed refill keeps the first composition.
func ComposeNative(
	ctx context.Context,
	database *store.Store,
	environment Env,
	view compose.View,
	data Data,
	live gather.Snapshot,
	native []gather.HerdrSeat,
) (compose.Output, error) {
	data, enrichErr := enrichNative(ctx, database, data, native)
	output := ComposeFleet(environment, view, data, live, native...)
	claims := output.LiveClaims()
	if data.CachedCounts == nil || len(claims.Transcripts)+len(claims.Rollouts) == 0 {
		return output, enrichErr
	}
	refilled, err := refillDefault(ctx, database, data, claims)
	if err != nil {
		return output, errors.Join(enrichErr, err)
	}
	return ComposeFleet(environment, view, refilled, live, native...), enrichErr
}

// enrichNative adds only native identities omitted by the capped default load.
// Kills and OpenCode sessions are already complete. A seat whose store read
// fails stays unenriched and is named in the joined error and marked
// TranscriptUnread in native, in place; the returned data always carries
// every other seat's enrichment.
func enrichNative(ctx context.Context, database *store.Store, data Data, native []gather.HerdrSeat) (Data, error) {
	if data.CachedCounts == nil || len(native) == 0 {
		return data, nil
	}
	transcripts := make(map[string]int, len(data.Transcripts))
	for index := range data.Transcripts {
		transcripts[data.Transcripts[index].UUID] = index
	}
	rollouts := make(map[string]struct{}, len(data.Rollouts))
	for index := range data.Rollouts {
		rollouts[data.Rollouts[index].ID] = struct{}{}
	}
	var failures []error
	for index := range native {
		seat := &native[index].Seat
		if seat.SessionID == "" {
			continue
		}
		switch pfmengine.ID(seat.Engine) {
		case pfmengine.Claude:
			// The whole continued-in chain: the seat carries its newest segment.
			if err := loadChain(ctx, database, &data, transcripts, seat.SessionID); err != nil {
				native[index].TranscriptUnread = true
				failures = append(failures, fmt.Errorf("native seat %s transcript: %w", seat.SeatID, err))
			}
		case pfmengine.Codex:
			if _, found := rollouts[seat.SessionID]; found {
				continue
			}
			rollout, found, err := database.Rollout(ctx, seat.SessionID)
			if err != nil {
				native[index].TranscriptUnread = true
				failures = append(failures, fmt.Errorf("native seat %s rollout: %w", seat.SeatID, err))
				continue
			}
			rollouts[seat.SessionID] = struct{}{}
			if found {
				data.Rollouts = append(data.Rollouts, rollout)
			}
		}
	}
	return data, errors.Join(failures...)
}

// refillDefault re-reads the capped default candidates and totals around
// claims and adds the candidates data lacks: data's first capped read, taken
// before any chat was known live, let each live chat among the newest take a
// resume slot, dropping that many of the oldest resumable chats a full frame
// lists. Every unclaimed candidate of the first read is one of the re-read's,
// so the merge stays inside the caps.
func refillDefault(ctx context.Context, database *store.Store, data Data, claims store.LiveClaims) (Data, error) {
	transcripts, rollouts, counts, err := database.DefaultCandidates(
		ctx,
		compose.ClaudeResumeCap,
		compose.CodexResumeCap,
		claims,
	)
	if err != nil {
		return data, fmt.Errorf("refill default candidates: %w", err)
	}
	loadedTranscripts := make(map[string]struct{}, len(data.Transcripts))
	for index := range data.Transcripts {
		loadedTranscripts[data.Transcripts[index].UUID] = struct{}{}
	}
	data.Transcripts = slices.Clip(data.Transcripts)
	for index := range transcripts {
		if _, found := loadedTranscripts[transcripts[index].UUID]; !found {
			data.Transcripts = append(data.Transcripts, transcripts[index])
		}
	}
	loadedRollouts := make(map[string]struct{}, len(data.Rollouts))
	for index := range data.Rollouts {
		loadedRollouts[data.Rollouts[index].ID] = struct{}{}
	}
	data.Rollouts = slices.Clip(data.Rollouts)
	for index := range rollouts {
		if _, found := loadedRollouts[rollouts[index].ID]; !found {
			data.Rollouts = append(data.Rollouts, rollouts[index])
		}
	}
	data.CachedCounts = &counts
	return data, nil
}

// composeNativeFleet is the shared native-read policy after a scan has loaded
// indexed data and, for a full scan, gathered its tmux snapshot, in request's
// view. Native database failure must never discard that healthy snapshot.
func composeNativeFleet(
	ctx context.Context,
	database *store.Store,
	environment Env,
	request Request,
	data Data,
	live gather.Snapshot,
) (compose.Output, []string) {
	native, failures, err := NativeSeats(ctx, environment.Paths, request.ReadOnly)
	if err != nil {
		failures = append(failures, err.Error())
	}
	// An unreadable continued-in chain or rollout leaves only its seat
	// unenriched: the scan error names it and every other row still composes.
	output, err := ComposeNative(ctx, database, environment, request.View, data, live, native)
	if err != nil {
		failures = append(failures, err.Error())
	}
	return output, failures
}
