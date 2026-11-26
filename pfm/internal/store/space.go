package store

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

// ChatSpaces is the space each chat the index holds sits in (`pfm chat space`),
// keyed by every id a row or a resume of that chat carries: the id it was
// recorded under (a live Herdr row's is its seat's session id), the tail a
// continued Claude chat resumes by and every segment before it, and every
// thread of a Codex lineage with
// the root its rows carry. Among a chat's records the newest wins under all of
// them, a cleared one included; a cleared chat has no entry. A live seat's chat
// counts as held before the index does (an empty chat has no transcript yet),
// so a space recorded at birth or before the first message places it. A record
// of a chat neither the index nor a live seat holds is left out, never deleted:
// the index is a cache a rebuild refills, so the record stands for the chat's
// return, and meanwhile it reserves no label (place.Fleet.Labeled).
func (s *Store) ChatSpaces(ctx context.Context) (map[string]string, error) {
	records, err := s.state.Spaces(ctx)
	if err != nil {
		return nil, err
	}
	spaces := make(map[string]string)
	if len(records) == 0 {
		return spaces, nil
	}
	rollouts, err := s.Rollouts(ctx)
	if err != nil {
		return nil, fmt.Errorf("read rollouts for chat spaces: %w", err)
	}
	transcripts, err := s.Transcripts(ctx)
	if err != nil {
		return nil, fmt.Errorf("read transcripts for chat spaces: %w", err)
	}
	seats, err := s.state.Seats(ctx)
	if err != nil {
		return nil, fmt.Errorf("read seats for chat spaces: %w", err)
	}
	live := make(map[string]bool, len(seats))
	for index := range seats {
		if seats[index].State == "live" && seats[index].SessionID != "" {
			live[seats[index].SessionID] = true
		}
	}
	byUUID := make(map[string]Transcript, len(transcripts))
	for i := range transcripts {
		byUUID[transcripts[i].UUID] = transcripts[i]
	}
	lookup := func(id string) (Transcript, bool, error) {
		transcript, found := byUUID[id]
		return transcript, found, nil
	}
	_, roots := ResolveCodexLineages(rollouts)
	lineages := make(map[string][]string)
	for id, root := range roots {
		lineages[root] = append(lineages[root], id)
	}
	// A chat is known by its tail (Claude) or its lineage root (Codex): its
	// newest record wins, at a tie the one recorded under that key itself.
	chats := make(map[string]fleetdb.ChatSpace)
	recorded := make(map[string][]string)
	for _, id := range slices.Sorted(maps.Keys(records)) {
		record := records[id]
		held, err := s.indexHolds(ctx, id)
		if err != nil {
			return nil, err
		}
		if !held && !live[id] {
			continue
		}
		chat, err := walkContinuation(id, lookup)
		if err != nil {
			return nil, fmt.Errorf("follow chat space %s: %w", id, err)
		}
		if root := roots[id]; root != "" {
			chat = root
		}
		current, found := chats[chat]
		if !found || record.UpdatedAt > current.UpdatedAt || record.UpdatedAt == current.UpdatedAt && id == chat {
			chats[chat] = record
		}
		recorded[chat] = append(recorded[chat], id)
	}
	// A continued Claude chat's every segment wears its space, the ones before
	// and between its recorded ids included.
	segments := make(map[string][]string)
	for i := range transcripts {
		if id := transcripts[i].UUID; transcripts[i].Superseded {
			tail, err := walkContinuation(id, lookup)
			if err != nil {
				return nil, fmt.Errorf("follow chat space segment %s: %w", id, err)
			}
			segments[tail] = append(segments[tail], id)
		}
	}
	for chat, record := range chats {
		if record.Space == "" {
			continue
		}
		for _, key := range slices.Concat([]string{chat}, recorded[chat], lineages[chat], segments[chat]) {
			spaces[key] = record.Space
		}
	}
	return spaces, nil
}

// indexHolds reports whether the index holds the chat id (indexedChat).
func (s *Store) indexHolds(ctx context.Context, id string) (bool, error) {
	var held bool
	if err := s.logged().QueryRowContext(ctx, "SELECT "+indexedChat("?1"), id).Scan(&held); err != nil {
		return false, fmt.Errorf("look up chat %s in the index: %w", id, err)
	}
	return held, nil
}
