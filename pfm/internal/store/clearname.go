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
func (s *Store) clearName(ctx context.Context, transcript Transcript) (Transcript, error) {
	if s.SharedDegraded() != nil {
		// Open already warns; doctor exposes SharedDegraded separately. The
		// rebuildable cache remains readable when operator state is unavailable.
		return transcript, nil
	}
	name, found, err := s.state.ClearName(ctx, transcript.UUID)
	if err != nil {
		return transcript, fmt.Errorf("read successor name for transcript %s: %w", transcript.UUID, err)
	}
	return s.applyClearName(ctx, transcript, name, found)
}

func (s *Store) applyClearName(
	ctx context.Context, transcript Transcript, name naming.ClearName, found bool,
) (Transcript, error) {
	if observed := name.ObserveTitle(transcript.CustomTitle); found && observed != name {
		name = observed
		if !s.readOnly {
			if err := s.state.PutClearName(ctx, transcript.UUID, name, s.clockNow().Unix()); err != nil {
				return transcript, fmt.Errorf("confirm clear title %s: %w", transcript.UUID, err)
			}
		}
	}
	if found {
		transcript.CustomTitle = name.Title(transcript.CustomTitle)
	}
	return transcript, nil
}

func (s *Store) clearNames(ctx context.Context, transcripts []Transcript) ([]Transcript, error) {
	if s.SharedDegraded() != nil || len(transcripts) == 0 {
		return transcripts, nil
	}
	names, err := s.state.ClearNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("read successor names: %w", err)
	}
	for i := range transcripts {
		name, found := names[transcripts[i].UUID]
		transcripts[i], err = s.applyClearName(ctx, transcripts[i], name, found)
		if err != nil {
			return nil, err
		}
	}
	return transcripts, nil
}
