package chat

import (
	"context"
	"fmt"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// SeatInspector is InspectSeat for a watch: it carries every transcript it
// reads from one poll to the next (transcript.Reader), so a poll reads what
// each seat appended since the last, not its whole transcript.
type SeatInspector struct {
	runtime     *pfmconfig.Runtime
	transcripts *transcript.Reader
	nativeIdle  map[string]nativeIdleObservation
	// proofs proves the native seats it inspects (zero: a herdr-shield reader per proof).
	proofs pfmcall.HerdrProofs
	// shield is the herdr-shield reader this inspector owns and Sweep holds for
	// each poll; nil when its proofs belong to a caller that holds its own passes.
	shield  *herdr.Shield
	release func()
}

// NewSeatInspector returns a watch's inspector: its native seats are proved
// through one herdr-shield reader that Sweep holds for each poll, so a poll
// lists each Herdr socket once however many seats it inspects.
func NewSeatInspector(runtime *pfmconfig.Runtime) *SeatInspector {
	inspector := &SeatInspector{runtime: runtime, transcripts: new(transcript.Reader), release: func() {}}
	if runtime != nil {
		inspector.shield = gather.HerdrShield(gather.NewProcFS(runtime.Paths.ProcRoot))
		inspector.proofs = pfmcall.HerdrProofs{Shield: inspector.shield}
	}
	return inspector
}

// shareProofs makes the inspector prove through proofs, whose owner holds its
// passes: Sweep then holds nothing.
func (inspector *SeatInspector) shareProofs(proofs pfmcall.HerdrProofs) {
	inspector.shield, inspector.proofs = nil, proofs
}

// Shield is the herdr-shield reader the inspector proves through, for a scan
// of the same poll to share (nil: a reader per proof).
func (inspector *SeatInspector) Shield() *herdr.Shield {
	return inspector.proofs.Shield
}

// Inspect is InspectSeat with the real pane capture.
func (inspector *SeatInspector) Inspect(
	ctx context.Context,
	target headless.Chat,
	now time.Time,
) (headless.Status, error) {
	reads, native := inspector.transcripts, nativeStatusObservation{}
	status, err := inspectTarget(ctx, inspector.runtime, target, now, nil, reads, inspector.proofs, &native)
	if target.Backend == spawn.BackendHerdr {
		inspector.observeNativeIdle(target, now, native, &status, err)
	}
	return status, err
}

// Rest is the transcript's own status of target, read through the transcripts
// Inspect carries: a pane Herdr tracks no agent for rests on its transcript's
// last turn, which Inspect read a moment before, so it costs no second read.
func (inspector *SeatInspector) Rest(
	ctx context.Context,
	target headless.Chat,
	now time.Time,
) (headless.Status, error) {
	return headless.InspectWith(ctx, target, now, inspector.transcripts)
}

// Sweep lets go of the transcripts not inspected since the last Sweep: a
// watch sweeps once per poll, so it holds only the seats it still watches.
func (inspector *SeatInspector) Sweep() {
	if inspector.shield != nil {
		inspector.release()
		inspector.release = inspector.shield.Hold()
	}
	inspector.transcripts.Sweep()
	for key, entry := range inspector.nativeIdle {
		if !entry.seen {
			delete(inspector.nativeIdle, key)
		} else {
			entry.seen = false
			inspector.nativeIdle[key] = entry
		}
	}
}

type nativeIdleObservation struct {
	identity string
	seq      uint64
	since    time.Time
	seen     bool
}

func (inspector *SeatInspector) observeNativeIdle(
	target headless.Chat,
	now time.Time,
	native nativeStatusObservation,
	status *headless.Status,
	readErr error,
) {
	if inspector.nativeIdle == nil {
		inspector.nativeIdle = make(map[string]nativeIdleObservation)
	}
	key := target.SeatID + "\x00" + target.ID + "\x00" + target.Name
	previous, found := inspector.nativeIdle[key]
	identityParts := []string{target.Socket, target.Pane, native.Terminal}
	if native.AgentSession != nil {
		session := native.AgentSession
		identityParts = append(identityParts, session.Source, session.Agent, session.Kind, session.Value)
	}
	identity := fmt.Sprintf("%q", identityParts)
	entry := nativeIdleObservation{identity: identity, seq: native.StateChangeSeq, seen: true}
	resting := readErr == nil && target.Live && (native.State == "idle" || native.State == "done")
	if resting {
		entry.since = now
		if found && previous.identity == entry.identity && previous.seq == entry.seq && !previous.since.IsZero() {
			entry.since = previous.since
		} else if !found && status.IdleSeconds > 0 {
			entry.since = now.Add(-time.Duration(status.IdleSeconds) * time.Second)
		}
		elapsed := max(int64(0), int64(now.Sub(entry.since)/time.Second))
		if native.TranscriptRest && status.IdleSeconds < elapsed {
			elapsed = status.IdleSeconds
			entry.since = now.Add(-time.Duration(elapsed) * time.Second)
		}
		status.IdleSeconds = elapsed
		status.RestID = fmt.Sprintf("%s:%d", identity, native.StateChangeSeq)
	}
	inspector.nativeIdle[key] = entry
}
