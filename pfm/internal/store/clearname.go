package store

import (
	"context"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/naming"
)

// ReserveClearName chooses and persists a successor name while the initialized
// transcript cache and shared decisions are locked in one SQLite transaction.
func (s *Store) ReserveClearName(ctx context.Context, id, original string) (naming.ClearName, error) {
	return s.state.ReserveClearName(ctx, id, original, s.path, s.clockNow().Unix())
}

// SeedClearTranscript makes a newborn successor visible without overwriting an
// index pass that may already have populated the row since the hook's lookup.
func (s *Store) SeedClearTranscript(ctx context.Context, transcript Transcript) error {
	if _, err := execWrite(ctx, s.logged(), `
INSERT INTO transcripts(uuid,path,cwd) VALUES(?,?,?) ON CONFLICT(uuid) DO NOTHING`,
		transcript.UUID, transcript.Path, transcript.CWD); err != nil {
		return fmt.Errorf("seed clear transcript %s: %w", transcript.UUID, err)
	}
	return nil
}

// clearName overlays pfm's durable successor name while Claude still carries
// the inherited title. It never rewrites a transcript or hides a later rename.
// A name it cannot read leaves the transcript on its engine title, warned.
func (s *Store) clearName(ctx context.Context, transcript Transcript) Transcript {
	if s.SharedDegraded() != nil {
		// Open already warns; doctor exposes SharedDegraded separately. The
		// rebuildable cache remains readable when operator state is unavailable.
		return transcript
	}
	name, found, err := s.state.ClearName(ctx, transcript.UUID)
	if err != nil {
		s.warningf("WARNING: pfm could not read the successor name of transcript %s; it shows its own title: %v\n",
			transcript.UUID, err)
		return transcript
	}
	return s.applyClearName(ctx, transcript, name, found)
}

// applyClearName overlays one decision. A title observation that cannot be
// stored is warned and retried on the next read; the overlay still applies.
func (s *Store) applyClearName(
	ctx context.Context, transcript Transcript, name naming.ClearName, found bool,
) Transcript {
	if observed := name.ObserveTitle(transcript.CustomTitle); found && observed != name {
		name = observed
		if !s.readOnly {
			if err := s.state.PutClearName(ctx, transcript.UUID, name, s.clockNow().Unix()); err != nil {
				s.warningf("WARNING: pfm could not confirm the clear title of transcript %s: %v\n",
					transcript.UUID, err)
			}
		}
	}
	if found {
		transcript.CustomTitle = name.Title(transcript.CustomTitle)
	}
	return transcript
}

// clearNames overlays every decision in one read. Names it cannot read leave
// the transcripts on their engine titles, warned, never an empty list.
func (s *Store) clearNames(ctx context.Context, transcripts []Transcript) []Transcript {
	if s.SharedDegraded() != nil || len(transcripts) == 0 {
		return transcripts
	}
	names, err := s.state.ClearNames(ctx)
	if err != nil {
		s.warningf("WARNING: pfm could not read the successor names; transcripts show their own titles: %v\n", err)
		return transcripts
	}
	for i := range transcripts {
		name, found := names[transcripts[i].UUID]
		transcripts[i] = s.applyClearName(ctx, transcripts[i], name, found)
	}
	return transcripts
}
