package headless

import (
	"bytes"
	"context"
	"errors"
	"path"
	"strings"
	"testing"
	"time"
)

func TestFleetWatchGlobAdoptsSeatsBornLater(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		transitions bool
		want        []string
	}{
		{"legacy", false, []string{"IDLE x-1 idle_seconds=3", "IDLE x-3 idle_seconds=3"}},
		{"transitions", true, []string{"SEEN x-1 idle idle_seconds=3", "SEEN x-3 idle idle_seconds=3"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			seats := newFakeSeats(map[string][]fakeStep{
				"x-1": {idleFor(3, "a")}, "x-3": {idleFor(3, "a")},
				"x-2": {idleFor(3, "a")}, "other": {idleFor(3, "a")},
			})
			lines, result, err := runFleet(t, func(cancel context.CancelFunc) FleetWatcher {
				return FleetWatcher{
					Targets: []string{"x-*"},
					List: listingAt(cancel, 4, func(poll int) ([]Chat, error) {
						chats := []Chat{liveChat("x-1"), liveChat("other"), {Name: "x-2"}}
						if poll >= 3 {
							chats = append(chats, liveChat("x-3"))
						}
						return chats, nil
					}),
					// The non-live x-2 is listed, and never inspected: it is not adopted.
					Inspect: seats.inspect,
				}
			}, WatchOptions{Transitions: testCase.transitions})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Watch() error = %v, want context.Canceled", err)
			}
			assertLines(t, lines, testCase.want)
			if seats.inspected("other") != 0 || seats.inspected("x-2") != 0 {
				t.Fatalf("a non-matching or non-live seat was adopted: other=%d x-2=%d",
					seats.inspected("other"), seats.inspected("x-2"))
			}
			if len(result.Errors) != 0 {
				t.Fatalf("Errors = %v", result.Errors)
			}
		})
	}
}

func TestFleetWatchGlobReadoptsARespawnedSeatWithAFreshSnapshot(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		transitions bool
		want        []string
	}{
		{"legacy", false, []string{"IDLE s idle_seconds=3", "EXIT s", "IDLE s idle_seconds=3"}},
		{"transitions", true, []string{
			"SEEN s idle idle_seconds=3", "EXIT s", "SEEN s idle idle_seconds=3",
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Polls: 1 live, 2 live but its server is gone, 3 absent, 4 live again.
			seats := newFakeSeats(map[string][]fakeStep{"s": {idleFor(3, "a"), gone(), idleFor(3, "a")}})
			lines, _, err := runFleet(t, func(cancel context.CancelFunc) FleetWatcher {
				return FleetWatcher{
					Targets: []string{"s*"},
					List: listingAt(cancel, 5, func(poll int) ([]Chat, error) {
						if poll == 3 {
							return nil, nil
						}
						return []Chat{liveChat("s")}, nil
					}),
					Inspect: seats.inspect,
				}
			}, WatchOptions{Transitions: testCase.transitions})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Watch() error = %v, want context.Canceled", err)
			}
			assertLines(t, lines, testCase.want)
		})
	}
}

func TestFleetWatchGlobSeatAbsentFromTheListingIsDead(t *testing.T) {
	seats := newFakeSeats(map[string][]fakeStep{"s": {idleFor(3, "a")}})
	exits := 0
	lines, result, err := runFleet(t, func(cancel context.CancelFunc) FleetWatcher {
		return FleetWatcher{
			Targets: []string{"s*"},
			List: listingAt(cancel, 3, func(poll int) ([]Chat, error) {
				if poll == 1 {
					return []Chat{liveChat("s")}, nil
				}
				return nil, nil
			}),
			Inspect: seats.inspect,
		}
	}, WatchOptions{OnExit: func(Status) error { exits++; return nil }})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Watch() error = %v, want context.Canceled", err)
	}
	assertLines(t, lines, []string{"IDLE s idle_seconds=3", "DEAD s"})
	if exits != 1 || result.Ended["s"].State != StateMissing {
		t.Fatalf("exits = %d, Ended = %#v", exits, result.Ended)
	}
}

func TestFleetWatchANamedTargetSuppressesTheGlobsDuplicate(t *testing.T) {
	seats := newFakeSeats(map[string][]fakeStep{"alpha": {idleFor(3, "a")}, "abe": {idleFor(3, "a")}})
	lines, _, err := runFleet(t, func(cancel context.CancelFunc) FleetWatcher {
		return FleetWatcher{
			Targets: []string{"alpha", "a*"},
			Resolve: seats.resolve,
			List: listingAt(cancel, 3, func(int) ([]Chat, error) {
				return []Chat{liveChat("alpha"), liveChat("abe")}, nil
			}),
			Inspect: seats.inspect,
		}
	}, WatchOptions{Transitions: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Watch() error = %v, want context.Canceled", err)
	}
	assertLines(t, lines, []string{"SEEN alpha idle idle_seconds=3", "SEEN abe idle idle_seconds=3"})
}

func TestFleetWatchRefusesAMalformedPatternBeforeTheFirstPoll(t *testing.T) {
	polled := false
	var out bytes.Buffer
	// A regression that accepts the pattern would watch forever: bound it.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := FleetWatcher{
		Targets: []string{"fine", "["},
		Resolve: func(context.Context, string) (Chat, bool, error) { polled = true; return Chat{}, false, nil },
		List:    func(context.Context) ([]Chat, error) { polled = true; return nil, nil },
	}.Watch(ctx, WatchOptions{Poll: time.Millisecond}, &out)
	if !errors.Is(err, path.ErrBadPattern) || !strings.Contains(err.Error(), `"["`) {
		t.Fatalf("Watch() error = %v, want ErrBadPattern naming the pattern", err)
	}
	if polled || out.Len() != 0 {
		t.Fatalf("polled = %v, output = %q, want a refusal before any poll", polled, out.String())
	}
}

func TestFleetWatchAFailedListingErrorsEveryGlobAndAdoptedSeat(t *testing.T) {
	seats := newFakeSeats(map[string][]fakeStep{"a1": {working("x")}})
	lines, result, err := runFleet(t, func(context.CancelFunc) FleetWatcher {
		return FleetWatcher{
			Targets: []string{"a*", "b*"},
			List: func() func(context.Context) ([]Chat, error) {
				poll := 0
				return func(context.Context) ([]Chat, error) {
					poll++
					if poll == 2 {
						return nil, errors.New("scan\nfailed")
					}
					return []Chat{liveChat("a1")}, nil
				}
			}(),
			Inspect: seats.inspect,
		}
	}, WatchOptions{})
	if err != nil {
		t.Fatalf("Watch() error = %v, want nil: every target ended", err)
	}
	assertLines(t, lines, []string{"ERROR a* scan failed", "ERROR b* scan failed", "ERROR a1 scan failed"})
	if len(result.Errors) != 3 || result.Errors["a1"] == nil || result.Errors["b*"] == nil {
		t.Fatalf("Errors = %v", result.Errors)
	}
}

// printedFleet is one live seat a watch follows, listed by a counting
// discovery and fingerprinted by a value the test sets per poll.
type printedFleet struct {
	seat   Chat
	lists  int
	sinces []time.Time
	prints []seatPrint // one per poll from the second on: the seat binds on the first
	asked  int
}

func (fleet *printedFleet) discover(since time.Time) Discovery {
	fleet.sinces = append(fleet.sinces, since)
	return Discovery{
		Resolve: func(context.Context, string) (Chat, bool, error) { return fleet.seat, true, nil },
		List: func(context.Context) ([]Chat, error) {
			fleet.lists++
			return []Chat{fleet.seat}, nil
		},
	}
}

func (fleet *printedFleet) fingerprint(_ context.Context, _ Chat) (string, bool) {
	printed := fleet.prints[min(fleet.asked, len(fleet.prints)-1)]
	fleet.asked++
	return printed.value, printed.ok
}

// TestFleetWatchReusesAFollowedSeatsRowWhileItsFingerprintHolds: a followed
// seat is listed (scanned) only when its fingerprint changed since the one
// read before its last listing, when it cannot be fingerprinted, and once
// every SeatPrintReuse polls; a poll that lists it scans after the
// fingerprint was read.
func TestFleetWatchReusesAFollowedSeatsRowWhileItsFingerprintHolds(t *testing.T) {
	t.Parallel()
	held := seatPrint{value: "a", ok: true}
	moved := seatPrint{value: "b", ok: true}
	for _, test := range []struct {
		name   string
		prints []seatPrint
		polls  int
		lists  int
	}{
		// poll 1 binds by Resolve; poll 2 lists against print a (the
		// baseline); polls 3-6 reuse it.
		{name: "an unchanged seat is listed once", prints: []seatPrint{held}, polls: 6, lists: 1},
		// polls 4 and 5 read b: 4 lists (a changed seat), 5 reuses.
		{name: "a changed seat is listed again", prints: []seatPrint{held, held, moved}, polls: 6, lists: 2},
		{name: "a seat that cannot be fingerprinted is listed every poll", prints: []seatPrint{{}}, polls: 6, lists: 5},
		{
			name: "an unchanged seat is listed again after SeatPrintReuse reuses", prints: []seatPrint{held},
			polls: 2 + SeatPrintReuse + 1, lists: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			steps := make([]fakeStep, test.polls)
			for index := range steps[:len(steps)-1] {
				steps[index] = working("a")
			}
			steps[len(steps)-1] = gone()
			seats := newFakeSeats(map[string][]fakeStep{"x": steps})
			fleet := &printedFleet{
				seat:   Chat{Name: "x", Socket: "cc-1-1-1", Pane: "%0", Live: true},
				prints: test.prints,
			}
			auto := newAutoClock()
			var out bytes.Buffer
			_, err := FleetWatcher{
				Targets: []string{"x"}, Discover: fleet.discover, Inspect: seats.inspect,
				Fingerprint: fleet.fingerprint, Clock: auto,
			}.Watch(context.Background(), WatchOptions{}, &out)
			if err != nil {
				t.Fatalf("Watch() error = %v", err)
			}
			if got := seats.inspected("x"); got != test.polls {
				t.Fatalf("inspected %d polls, want %d: a reused row is still inspected every poll", got, test.polls)
			}
			if fleet.lists != test.lists {
				t.Fatalf("listed %d times over %d polls, want %d", fleet.lists, test.polls, test.lists)
			}
			if !strings.Contains(out.String(), "EXIT x") {
				t.Fatalf("lines %q, want the seat's EXIT", out.String())
			}
		})
	}
}

// TestFleetWatchListsAChangedSeatAfterItsFingerprint: the poll that lists a
// seat it could not reuse asks for a scan begun once its fingerprint read has
// ended, never its tick nor the read's start — a scan (another watch's
// shared one) begun before the read ended could miss a change the
// fingerprint already holds, and pair the new fingerprint with the old row.
func TestFleetWatchListsAChangedSeatAfterItsFingerprint(t *testing.T) {
	t.Parallel()
	seats := newFakeSeats(map[string][]fakeStep{"x": {working("a"), working("a"), gone()}})
	fleet := &printedFleet{
		seat:   Chat{Name: "x", Socket: "cc-1-1-1", Pane: "%0", Live: true},
		prints: []seatPrint{{value: "a", ok: true}},
	}
	auto := newAutoClock()
	// Every reading of the clock is a millisecond later than the one before:
	// a poll wakes just after its tick, and reads its fingerprints after that.
	var drift time.Duration
	now := func() time.Time {
		drift += time.Millisecond
		return auto.Now().Add(drift)
	}
	var reads []time.Time
	fingerprint := func(ctx context.Context, chat Chat) (string, bool) {
		reads = append(reads, now())
		return fleet.fingerprint(ctx, chat)
	}
	var out bytes.Buffer
	_, err := FleetWatcher{
		Targets: []string{"x"}, Discover: fleet.discover, Inspect: seats.inspect, Fingerprint: fingerprint,
		Now: now, Clock: auto,
	}.Watch(context.Background(), WatchOptions{}, &out)
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	// Poll 2 is the first with a bound seat and no baseline: it lists.
	if len(reads) == 0 || len(fleet.sinces) < 2 {
		t.Fatalf("fingerprint reads %v, discoveries since %v: want poll 2 to read and list", reads, fleet.sinces)
	}
	since := fleet.sinces[1]
	if since.UnixNano()%int64(MinPoll) == 0 || !since.After(reads[0]) {
		t.Fatalf("poll 2 listed since %v (fingerprint read at %v): want after the read had ended",
			since, reads[0])
	}
}

// TestFleetWatchFollowsASeatThroughASocketlessListing (D6): a probe that timed
// out under load lists a live seat without its socket for a poll; the named
// target and the glob follow it through that poll and back as one seat, with
// no EXIT, no DEAD and no second SEEN.
func TestFleetWatchFollowsASeatThroughASocketlessListing(t *testing.T) {
	t.Parallel()
	withSocket := Chat{Name: "x", ID: "sid-x", Socket: "cc-1-1-1", Pane: "%0", Live: true}
	without := Chat{Name: "x", ID: "sid-x", Live: true}
	// First listed without its socket (bound or adopted so), then with it, then
	// without it again, then with it.
	listings := [][]Chat{{without}, {withSocket}, {without}, {withSocket}, {withSocket}}
	for _, target := range []string{"x", "x*"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			steps := []fakeStep{working("a"), working("a"), working("a"), working("a"), idleFor(30, "done")}
			seats := newFakeSeats(map[string][]fakeStep{"x": steps})
			poll := 0
			discover := func(time.Time) Discovery {
				listing := listings[min(poll, len(listings)-1)]
				poll++
				return Discovery{
					Resolve: func(context.Context, string) (Chat, bool, error) { return listing[0], true, nil },
					List:    func(context.Context) ([]Chat, error) { return listing, nil },
					Unreadable: func() map[string]string {
						if listing[0].Socket == "" {
							return map[string]string{"cc-1-1-1": "server unresponsive"}
						}
						return nil
					},
				}
			}
			auto := newAutoClock()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var out bytes.Buffer
			inspect := func(ctx context.Context, chat Chat, now time.Time) (Status, error) {
				if seats.inspected("x") == len(steps)-1 {
					defer cancel()
				}
				return seats.inspect(ctx, chat, now)
			}
			_, err := FleetWatcher{Targets: []string{target}, Discover: discover, Inspect: inspect, Clock: auto}.
				Watch(ctx, WatchOptions{Transitions: true}, &out)
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Watch() error = %v", err)
			}
			lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
			if len(lines) != 2 || !strings.HasPrefix(lines[0], "SEEN x working") ||
				!strings.HasPrefix(lines[1], "IDLE x") {
				t.Fatalf("lines %q, want one SEEN and the IDLE: one seat through the socketless polls", lines)
			}
		})
	}
}
