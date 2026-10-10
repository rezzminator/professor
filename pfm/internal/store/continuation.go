package store

import "context"

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
		to, seen := from, map[string]struct{}{from: {}}
		for {
			transcript, found, err := s.Transcript(ctx, to)
			if err != nil {
				s.warningf("WARNING: pfm could not follow the continuation of session %s from %s: %v; "+
					"its schedules stay on %s until the next index pass\n", from, to, err, from)
				to = from
				break
			}
			if !found || !transcript.Superseded {
				break
			}
			if _, cycle := seen[transcript.ContinuedIn]; cycle {
				break
			}
			seen[transcript.ContinuedIn] = struct{}{}
			to = transcript.ContinuedIn
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
