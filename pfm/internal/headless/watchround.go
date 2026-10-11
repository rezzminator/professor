package headless

import (
	"context"
	"fmt"
	"path"
	"sort"
	"time"
)

// pollRound is one poll over one shared discovery: every live named target in
// argument order, then the seats the globs adopted, sorted by name.
//
// Each followed seat's fingerprint is read first. When any of them cannot
// reuse its row, the poll's scan must begin after those reads have ended
// (since moves up to their end): a fingerprint is trusted only against a scan
// begun after it was read whole, so a change landing during the read — in the
// fingerprint, yet missing from a scan another watch began meanwhile — can
// never pair a new fingerprint with an old row.
func (run *fleetRun) pollRound(ctx context.Context, since time.Time) error {
	prints, taken := run.fingerprints(ctx)
	for seat, printed := range prints {
		if !reusable(seat, printed) && taken.After(since) {
			since = taken
		}
	}
	round := run.discovery(since)
	for _, seat := range run.named {
		if seat.ended {
			continue
		}
		if err := run.pollNamed(ctx, round, seat, prints[seat]); err != nil {
			return err
		}
	}
	if run.liveGlobs() == 0 {
		return nil
	}
	return run.pollGlobs(ctx, round)
}

// discovery is this poll's: the watcher's own when it opens one, else its
// Resolve and List called as they are.
func (run *fleetRun) discovery(since time.Time) Discovery {
	if run.discover != nil {
		return run.discover(since)
	}
	return Discovery{Resolve: run.resolve, List: run.list}
}

// fingerprints reads every followed named seat's fingerprint, and when the
// reads had all ended.
func (run *fleetRun) fingerprints(ctx context.Context) (map[*watchedSeat]seatPrint, time.Time) {
	if run.fingerprint == nil {
		return nil, time.Time{}
	}
	prints := map[*watchedSeat]seatPrint{}
	for _, seat := range run.named {
		if seat.ended || seat.bound == nil {
			continue
		}
		value, ok := run.fingerprint(ctx, *seat.bound)
		prints[seat] = seatPrint{value: value, ok: ok}
	}
	return prints, run.now()
}

// reusable says a followed seat's row still stands: its fingerprint is the one
// taken before the scan that row came from, and that scan is recent enough.
func reusable(seat *watchedSeat, printed seatPrint) bool {
	return printed.ok && seat.print != "" && printed.value == seat.print && seat.reused < SeatPrintReuse
}

// pollNamed reads one named target. Until it is first seen live it is
// resolved by name; from then on the watch follows that seat in the poll's
// listing, whatever its name or conversation has become — or, while the
// seat's fingerprint holds, on the row it was last listed with.
func (run *fleetRun) pollNamed(ctx context.Context, round Discovery, seat *watchedSeat, printed seatPrint) error {
	if seat.bound != nil && round.List != nil {
		if reusable(seat, printed) {
			seat.reused++
			return run.advance(ctx, seat, *seat.bound, true)
		}
		seat.print, seat.reused = "", 0
		bound, before := seat.bound, *seat.bound
		if err := run.followBound(ctx, round, seat); err != nil {
			return err
		}
		// The scan began after the fingerprint was read whole (pollRound),
		// shared or not: when it found the seat live (follow rebinds it)
		// exactly as the fingerprint saw it, the pair is the next polls'
		// baseline.
		if printed.ok && !seat.ended && seat.bound != bound && *seat.bound == before {
			seat.print = printed.value
		}
		return nil
	}
	chat, found, err := round.Resolve(ctx, seat.name)
	if err != nil {
		return run.failSeat(ctx, seat, err)
	}
	if found && chat.Live {
		if seat.bound != nil && !sameSeat(*seat.bound, chat) && !sameConversation(*seat.bound, chat) {
			// No listing to find the followed seat in: another seat answering
			// to the name means the followed one is gone.
			return run.leaveBound(ctx, seat, nil)
		}
		return run.follow(ctx, seat, chat)
	}
	if why := run.unreadable(round, seat); why != "" {
		return run.deferSeat(ctx, seat, why)
	}
	return run.advance(ctx, seat, chat, found)
}

// followBound finds a named target's seat in this poll's listing.
func (run *fleetRun) followBound(ctx context.Context, round Discovery, seat *watchedSeat) error {
	chats, err := round.List(ctx)
	if err != nil {
		return run.failSeat(ctx, seat, err)
	}
	bound := *seat.bound
	for index := range chats {
		chat := chats[index]
		if chat.Live && (sameSeat(bound, chat) || (socketless(bound) && sameConversation(bound, chat))) {
			return run.follow(ctx, seat, chat)
		}
	}
	// The seat's socket file vanished under a running engine: the fleet still
	// lists the conversation live, by its process, with no socket. That is the
	// same chat still working, not a respawn (which runs on a socket of its own).
	for index := range chats {
		chat := chats[index]
		if chat.Live && chat.Socket == "" && chat.Pane == "" && bound.ID != "" && chat.ID == bound.ID {
			seat.unread = 0
			return run.advance(ctx, seat, chat, true)
		}
	}
	if why := run.unreadable(round, seat); why != "" {
		return run.deferSeat(ctx, seat, why)
	}
	return run.leaveBound(ctx, seat, chats)
}

// follow reads the live seat a named target follows, binding it on first
// sight and claiming it from the globs.
func (run *fleetRun) follow(ctx context.Context, seat *watchedSeat, chat Chat) error {
	if seat.bound != nil {
		delete(run.claimed, claimKey(*seat.bound))
	}
	bound := chat
	seat.bound = &bound
	run.claimed[claimKey(chat)] = true
	return run.advance(ctx, seat, chat, true)
}

// leaveBound ends a followed seat that is gone: EXIT, on the row its own
// conversation left behind when the listing still has it.
func (run *fleetRun) leaveBound(ctx context.Context, seat *watchedSeat, chats []Chat) error {
	bound := *seat.bound
	for index := range chats {
		chat := chats[index]
		if !chat.Live && bound.ID != "" && chat.ID == bound.ID {
			chat.Name = bound.Name
			return run.advance(ctx, seat, chat, true)
		}
	}
	seat.unread = 0
	seat.status = Status{
		Name: seat.name, Engine: bound.Engine, CWD: bound.CWD, SessionID: bound.ID,
		Socket: bound.Socket, State: StateDead,
	}
	if run.options.Transitions && !seat.seen {
		return run.snapshot(ctx, seat)
	}
	return run.leave(seat, "EXIT")
}

// unreadable is why this poll cannot say a seat is gone: its own server (or,
// for a seat never yet seen live, any chat server) could not be read.
func (run *fleetRun) unreadable(round Discovery, seat *watchedSeat) string {
	if round.Unreadable == nil {
		return ""
	}
	sockets := round.Unreadable()
	if seat.bound != nil {
		if why, ok := sockets[seat.bound.Socket]; ok {
			return seat.bound.Socket + ": " + why
		}
		return ""
	}
	names := make([]string, 0, len(sockets))
	for socket := range sockets {
		names = append(names, socket)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0] + ": " + sockets[names[0]]
}

// deferSeat holds a seat's verdict for a poll its server could not be read
// in, and fails it once that has lasted UnreadablePolls polls.
func (run *fleetRun) deferSeat(ctx context.Context, seat *watchedSeat, why string) error {
	seat.unread++
	if seat.unread < UnreadablePolls {
		return nil
	}
	return run.failSeat(ctx, seat, fmt.Errorf(
		"tmux server unreadable for %d polls in a row, so whether the chat lives is unknown: %s", seat.unread, why))
}

// pollGlobs lists the fleet once for every glob: each live seat whose name a
// glob matches is adopted on its own, followed while its name and seat hold,
// and ended (EXIT on its own conversation's row, DEAD when it left none)
// when they no longer do.
func (run *fleetRun) pollGlobs(ctx context.Context, round Discovery) error {
	chats, err := round.List(ctx)
	if err != nil {
		return run.failGlobs(ctx, err)
	}
	live := map[seatKey]Chat{}
	for index := range chats {
		if chats[index].Live {
			live[keyOf(chats[index])] = chats[index]
		}
	}
	// A seat that ended is let go only once a listing no longer shows it live,
	// so a respawn under the same name is a fresh seat with a fresh SEEN.
	for key, seat := range run.adopted {
		if _, ok := live[key]; seat.ended && (!ok || !run.globMatches(key.name)) {
			delete(run.adopted, key)
		}
	}
	run.rekeyAdopted(chats, live)
	for index := range chats {
		chat := chats[index]
		key := keyOf(chat)
		if !chat.Live || run.adopted[key] != nil || run.claimed[claimKey(chat)] || !run.globMatches(chat.Name) {
			continue
		}
		bound := chat
		run.adopted[key] = &watchedSeat{name: chat.Name, bound: &bound}
	}
	for _, key := range run.liveAdopted() {
		seat := run.adopted[key]
		if chat, ok := live[key]; ok {
			bound := chat
			seat.bound = &bound
			if err := run.advance(ctx, seat, chat, true); err != nil {
				return err
			}
			continue
		}
		if why := run.unreadable(round, seat); why != "" {
			if err := run.deferSeat(ctx, seat, why); err != nil {
				return err
			}
			continue
		}
		chat, found := leftRow(chats, *seat.bound)
		if err := run.advance(ctx, seat, chat, found); err != nil {
			return err
		}
	}
	return nil
}

// rekeyAdopted moves an adopted seat the listing no longer shows under its key
// onto the live row of its own conversation when one of the two was listed
// without its socket (sameConversation), so the glob follows one seat on —
// never ends it and adopts it afresh.
func (run *fleetRun) rekeyAdopted(chats []Chat, live map[seatKey]Chat) {
	for _, key := range run.liveAdopted() {
		if _, listed := live[key]; listed {
			continue
		}
		seat := run.adopted[key]
		for index := range chats {
			chat := chats[index]
			moved := keyOf(chat)
			if !chat.Live || run.adopted[moved] != nil || !sameConversation(*seat.bound, chat) {
				continue
			}
			delete(run.adopted, key)
			run.adopted[moved] = seat
			break
		}
	}
}

// leftRow is the row a seat's own conversation left once the seat is gone:
// its session id, never merely its name, so the EXIT (and the hook it runs)
// carries the conversation that ended.
func leftRow(chats []Chat, bound Chat) (Chat, bool) {
	for index := range chats {
		chat := chats[index]
		if chat.Live {
			continue
		}
		if bound.ID != "" && chat.ID == bound.ID {
			chat.Name = bound.Name
			return chat, true
		}
		if bound.ID == "" && chat.Name == bound.Name && chat.Socket == bound.Socket && chat.Pane == bound.Pane {
			return chat, true
		}
	}
	return Chat{}, false
}

func (run *fleetRun) globMatches(name string) bool {
	for _, glob := range run.globs {
		if glob.ended {
			continue
		}
		if ok, err := path.Match(glob.name, name); err == nil && ok {
			return true
		}
	}
	return false
}

// liveAdopted is the adopted seats still being followed, sorted by name, then
// socket and pane.
func (run *fleetRun) liveAdopted() []seatKey {
	keys := make([]seatKey, 0, len(run.adopted))
	for key, seat := range run.adopted {
		if !seat.ended {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].name != keys[right].name {
			return keys[left].name < keys[right].name
		}
		if keys[left].socket != keys[right].socket {
			return keys[left].socket < keys[right].socket
		}
		return keys[left].pane < keys[right].pane
	})
	return keys
}
