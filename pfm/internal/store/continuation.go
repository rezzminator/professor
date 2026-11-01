package store

import (
	"context"
	"fmt"
)

// CarryContinuedSchedules moves every schedule whose chat Claude Code
// continued in another session onto the newest segment of that chat's
// continued-in chain, through fleetdb's RekeySchedules, the same move a
// reload's carry makes. A segment counts as continued only while it is
// Superseded — its successor indexed, the rule the picker follows — so a
// schedule never moves onto a chat pfm cannot show yet.
//
// It re-derives from the schedules on every pass, so a schedule set later on a
// superseded id follows too, and a move that failed is retried by the next
// pass: each failure is warned with its ids and cause, never fatal to the
// index. A degraded shared state was already warned when the store opened.
func (s *Store) CarryContinuedSchedules(ctx context.Context) {
	if s.state.Degraded() != nil {
		return
	}
	schedules, err := s.state.Schedules(ctx)
	if err != nil {
		s.warningf("WARNING: pfm could not read the schedules in %s to carry them across continued chats: %v\n",
			s.state.Path(), err)
		return
	}
	followed := make(map[string]struct{}, len(schedules))
	for index := range schedules {
		from := schedules[index].SessionID
		if _, done := followed[from]; done {
			continue
		}
		followed[from] = struct{}{}
		to, err := s.ContinuationTail(ctx, from)
		if err != nil {
			s.warningf("WARNING: pfm could not follow the continuation of session %s %v; "+
				"its schedules stay on %s until the next index pass\n", from, err, from)
			continue
		}
		if to == from {
			continue
		}
		if _, err := s.state.RekeySchedules(ctx, from, to); err != nil {
			s.warningf("WARNING: pfm could not move the schedules of session %s to %s, the session Claude Code "+
				"continued it in: %v; the next index pass retries\n", from, to, err)
		}
	}
}

// ContinuationTail follows id's continued-in chain to its newest segment: the
// session a resume of id must name, since resuming a superseded id reopens a
// segment Claude Code already moved on from. A segment counts as continued
// only while it is Superseded, its successor indexed, so the tail is always a
// chat pfm can show; an id the index lacks is its own tail. A cycle stops at
// the last new segment.
func (s *Store) ContinuationTail(ctx context.Context, start string) (string, error) {
	id, seen := start, map[string]struct{}{start: {}}
	for {
		transcript, found, err := s.Transcript(ctx, id)
		if err != nil {
			return "", fmt.Errorf("from %s at %s: %w", start, id, err)
		}
		if !found || !transcript.Superseded {
			return id, nil
		}
		if _, cycle := seen[transcript.ContinuedIn]; cycle {
			return id, nil
		}
		seen[transcript.ContinuedIn] = struct{}{}
		id = transcript.ContinuedIn
	}
}
