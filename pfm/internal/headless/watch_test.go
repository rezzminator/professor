package headless

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStep is one Inspect answer.
type fakeStep struct {
	status Status
	err    error
}

func working(last string) fakeStep { return fakeStep{status: Status{State: StateWorking, Last: last}} }
func blocked() fakeStep            { return fakeStep{status: Status{State: StateBlocked}} }
func idleFor(seconds int64, last string) fakeStep {
	return fakeStep{status: Status{State: StateIdle, IdleSeconds: seconds, Last: last}}
}

func erroredFor(seconds int64, kind string) fakeStep {
	return fakeStep{status: Status{State: StateError, IdleSeconds: seconds, Error: kind}}
}
func gone() fakeStep { return fakeStep{status: Status{State: StateDead}} }

// quietFor is a working seat whose transcript has been unchanged for seconds.
func quietFor(seconds int64, last string) fakeStep {
	return fakeStep{status: Status{State: StateWorking, QuietSeconds: seconds, Last: last}}
}

// fakeSeats scripts Resolve and Inspect per seat name: the Nth Inspect of a
// seat answers its Nth step, and the last step repeats.
type fakeSeats struct {
	mu         sync.Mutex
	steps      map[string][]fakeStep
	calls      map[string]int
	resolves   map[string]int
	missing    map[string]bool
	resolveErr map[string]error
}

func newFakeSeats(steps map[string][]fakeStep) *fakeSeats {
	return &fakeSeats{
		steps:      steps,
		calls:      map[string]int{},
		resolves:   map[string]int{},
		missing:    map[string]bool{},
		resolveErr: map[string]error{},
	}
}

func (seats *fakeSeats) resolve(_ context.Context, name string) (Chat, bool, error) {
	seats.mu.Lock()
	defer seats.mu.Unlock()
	seats.resolves[name]++
	if err := seats.resolveErr[name]; err != nil {
		return Chat{}, false, err
	}
	if seats.missing[name] {
		return Chat{}, false, nil
	}
	return Chat{Name: name, Live: true}, true, nil
}

func (seats *fakeSeats) inspect(_ context.Context, chat Chat, _ time.Time) (Status, error) {
	seats.mu.Lock()
	defer seats.mu.Unlock()
	steps := seats.steps[chat.Name]
	if len(steps) == 0 {
		return Status{}, fmt.Errorf("fake: no steps for %q", chat.Name)
	}
	index := min(seats.calls[chat.Name], len(steps)-1)
	seats.calls[chat.Name]++
	step := steps[index]
	step.status.Name = chat.Name
	return step.status, step.err
}

func (seats *fakeSeats) inspected(name string) int {
	seats.mu.Lock()
	defer seats.mu.Unlock()
	return seats.calls[name]
}

// runFleet runs a watch under a deadline and returns its lines. build gets the
// cancel func so a fake List can end a glob watch after N polls.
func runFleet(
	t *testing.T,
	build func(cancel context.CancelFunc) FleetWatcher,
	options WatchOptions,
) ([]string, WatchResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	options.Poll = time.Millisecond
	var out bytes.Buffer
	result, err := build(cancel).Watch(ctx, options, &out)
	trimmed := strings.TrimSpace(out.String())
	if trimmed == "" {
		return nil, result, err
	}
	return strings.Split(trimmed, "\n"), result, err
}

func namedFleet(seats *fakeSeats, targets ...string) func(context.CancelFunc) FleetWatcher {
	return func(context.CancelFunc) FleetWatcher {
		return FleetWatcher{Targets: targets, Resolve: seats.resolve, Inspect: seats.inspect}
	}
}

func assertLines(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// listingAt scripts List by poll number (1-based) and cancels the watch after
// stopAt polls, once that poll's listing has been handed over.
func listingAt(
	cancel context.CancelFunc,
	stopAt int,
	at func(poll int) ([]Chat, error),
) func(context.Context) ([]Chat, error) {
	poll := 0
	return func(context.Context) ([]Chat, error) {
		poll++
		if poll >= stopAt {
			cancel()
		}
		return at(poll)
	}
}

func liveChat(name string) Chat { return Chat{Name: name, Live: true} }

func TestFleetWatchTwoNamedTargetsEndIndependently(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		transitions bool
		want        []string
	}{
		{"legacy", false, []string{"EXIT a", "IDLE b idle_seconds=5", "EXIT b"}},
		{"transitions", true, []string{
			"SEEN a working", "SEEN b working", "EXIT a", "IDLE b idle_seconds=5", "EXIT b",
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			seats := newFakeSeats(map[string][]fakeStep{
				"a": {working("x"), gone()},
				"b": {working("x"), working("x"), idleFor(5, "y"), idleFor(5, "y"), gone()},
			})
			var exits []string
			lines, result, err := runFleet(t, namedFleet(seats, "a", "b"), WatchOptions{
				Transitions: testCase.transitions,
				OnExit:      func(status Status) error { exits = append(exits, status.Name); return nil },
			})
			if err != nil {
				t.Fatalf("Watch() error = %v", err)
			}
			assertLines(t, lines, testCase.want)
			if !reflect.DeepEqual(exits, []string{"a", "b"}) {
				t.Fatalf("OnExit seats = %v, want [a b]", exits)
			}
			if result.Ended["a"].State != StateDead || result.Ended["b"].State != StateDead || len(result.Errors) != 0 {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestFleetWatchAnErrorEndsOnlyItsTarget(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		setup func(*fakeSeats)
		want  string
	}{
		{"resolve", func(seats *fakeSeats) {
			seats.resolveErr["bad"] = errors.New("scan failed:\n\tdisk\t  gone")
		}, "ERROR bad scan failed: disk gone"},
		{"inspect", func(seats *fakeSeats) {
			seats.steps["bad"] = []fakeStep{{err: errors.New("read transcript:\nno such file")}}
		}, "ERROR bad read transcript: no such file"},
	} {
		for _, transitions := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/transitions=%v", testCase.name, transitions), func(t *testing.T) {
				seats := newFakeSeats(map[string][]fakeStep{
					"good": {working("x"), working("x"), gone()},
				})
				testCase.setup(seats)
				hooks := 0
				lines, result, err := runFleet(t, namedFleet(seats, "bad", "good"), WatchOptions{
					Transitions: transitions,
					OnIdle:      func(Status) error { hooks++; return nil },
					OnExit:      func(Status) error { hooks++; return nil },
				})
				if err != nil {
					t.Fatalf("Watch() error = %v", err)
				}
				want := []string{testCase.want, "EXIT good"}
				if transitions {
					want = []string{testCase.want, "SEEN good working", "EXIT good"}
				}
				assertLines(t, lines, want)
				if result.Errors["bad"] == nil || result.Errors["good"] != nil {
					t.Fatalf("Errors = %v, want only bad", result.Errors)
				}
				if hooks != 1 {
					t.Fatalf("hooks fired %d times, want 1 (EXIT good only: an ERROR runs no hook)", hooks)
				}
			})
		}
	}
}

func TestWatcherReturnsTheOriginalErrorAfterPrintingIt(t *testing.T) {
	cause := errors.New("scan\nfailed")
	var out bytes.Buffer
	_, err := Watcher{
		Name: "seat",
		Resolve: func(context.Context) (Chat, bool, error) {
			return Chat{}, false, cause
		},
	}.Watch(context.Background(), WatchOptions{Poll: time.Millisecond}, &out)
	if !errors.Is(err, cause) {
		t.Fatalf("Watch() error = %v, want the resolver's own", err)
	}
	if out.String() != "ERROR seat scan failed\n" {
		t.Fatalf("output = %q", out.String())
	}
}

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

func TestTransitionsAnnounceEachChangeOnce(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		steps []fakeStep
		after time.Duration
		want  []string
	}{
		{
			"idle working blocked idle",
			[]fakeStep{idleFor(9, "a"), working("a"), working("a"), blocked(), blocked(), idleFor(4, "b"), idleFor(4, "b"), gone()},
			0,
			[]string{
				"SEEN x idle idle_seconds=9", "WORKING x", "BLOCKED x", "IDLE x idle_seconds=4", "EXIT x",
			},
		},
		{
			"a seat idle at first sight is quiet until it works",
			[]fakeStep{idleFor(7, "a"), idleFor(8, "a"), idleFor(9, "a"), gone()},
			0,
			[]string{"SEEN x idle idle_seconds=7", "EXIT x"},
		},
		{
			"a seat idle at first sight announces its next rest",
			[]fakeStep{idleFor(7, "a"), working("a"), idleFor(1, "b"), gone()},
			0,
			[]string{"SEEN x idle idle_seconds=7", "WORKING x", "IDLE x idle_seconds=1", "EXIT x"},
		},
		{
			"a rest waits for idle-after",
			[]fakeStep{working("a"), idleFor(1, "b"), idleFor(2, "b"), idleFor(6, "b"), gone()},
			5 * time.Second,
			[]string{"SEEN x working", "IDLE x idle_seconds=6", "EXIT x"},
		},
		{
			"an errored turn carries its kind",
			[]fakeStep{working("a"), erroredFor(3, "server_error"), gone()},
			0,
			[]string{"SEEN x working", "IDLE x idle_seconds=3 error=server_error", "EXIT x"},
		},
		{
			"a first-sight error names its kind",
			[]fakeStep{erroredFor(3, "server_error"), gone()},
			0,
			[]string{"SEEN x error idle_seconds=3 error=server_error", "EXIT x"},
		},
		{
			"a turn that began and ended between polls",
			[]fakeStep{idleFor(1, "a"), idleFor(1, "a"), idleFor(1, "b"), gone()},
			0,
			[]string{"SEEN x idle idle_seconds=1", "WORKING x", "IDLE x idle_seconds=1", "EXIT x"},
		},
		{
			"a missed turn whose rest is still young",
			[]fakeStep{idleFor(10, "a"), idleFor(1, "b"), idleFor(6, "b"), gone()},
			5 * time.Second,
			[]string{"SEEN x idle idle_seconds=10", "WORKING x", "IDLE x idle_seconds=6", "EXIT x"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			seats := newFakeSeats(map[string][]fakeStep{"x": testCase.steps})
			lines, _, err := runFleet(t, namedFleet(seats, "x"), WatchOptions{
				Transitions: true,
				IdleAfter:   testCase.after,
			})
			if err != nil {
				t.Fatalf("Watch() error = %v", err)
			}
			assertLines(t, lines, testCase.want)
		})
	}
}

// TestTransitionsAnnounceAQuietSeatOnce pins --quiet-after: a working seat
// whose transcript sat unchanged for QuietAfter is QUIET once, WORKING again
// when the transcript moves, and never quiet while QuietAfter is zero.
func TestTransitionsAnnounceAQuietSeatOnce(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		steps []fakeStep
		quiet time.Duration
		want  []string
	}{
		{
			"quiet at the bound, not before",
			[]fakeStep{quietFor(0, "a"), quietFor(9, "a"), quietFor(10, "a"), quietFor(12, "a"), gone()},
			10 * time.Second,
			[]string{"SEEN x working", "QUIET x quiet_seconds=10", "EXIT x"},
		},
		{
			"working again after the transcript moves",
			[]fakeStep{quietFor(0, "a"), quietFor(10, "a"), quietFor(11, "a"), quietFor(0, "b"), quietFor(10, "b"), gone()},
			10 * time.Second,
			[]string{"SEEN x working", "QUIET x quiet_seconds=10", "WORKING x", "QUIET x quiet_seconds=10", "EXIT x"},
		},
		{
			"a seat quiet at first sight",
			[]fakeStep{quietFor(30, "a"), quietFor(31, "a"), quietFor(0, "b"), gone()},
			10 * time.Second,
			[]string{"SEEN x working quiet_seconds=30", "WORKING x", "EXIT x"},
		},
		{
			"a quiet seat that answers is idle",
			[]fakeStep{quietFor(0, "a"), quietFor(10, "a"), idleFor(1, "b"), gone()},
			10 * time.Second,
			[]string{"SEEN x working", "QUIET x quiet_seconds=10", "IDLE x idle_seconds=1", "EXIT x"},
		},
		{
			"off when QuietAfter is zero",
			[]fakeStep{quietFor(0, "a"), quietFor(900, "a"), gone()},
			0,
			[]string{"SEEN x working", "EXIT x"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			seats := newFakeSeats(map[string][]fakeStep{"x": testCase.steps})
			lines, _, err := runFleet(t, namedFleet(seats, "x"), WatchOptions{
				Transitions: true,
				QuietAfter:  testCase.quiet,
			})
			if err != nil {
				t.Fatalf("Watch() error = %v", err)
			}
			assertLines(t, lines, testCase.want)
		})
	}
}

func TestTransitionsSeenNotAliveEndsTheTargetQuietly(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		setup func(*fakeSeats)
		want  string
		state string
	}{
		{"not found", func(seats *fakeSeats) { seats.missing["x"] = true }, "SEEN x not-found", StateMissing},
		{"dead", func(seats *fakeSeats) { seats.steps["x"] = []fakeStep{gone()} }, "SEEN x dead", StateDead},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			seats := newFakeSeats(map[string][]fakeStep{})
			testCase.setup(seats)
			exits := 0
			lines, result, err := runFleet(t, namedFleet(seats, "x"), WatchOptions{
				Transitions: true,
				OnExit:      func(Status) error { exits++; return nil },
			})
			if err != nil {
				t.Fatalf("Watch() error = %v", err)
			}
			assertLines(t, lines, []string{testCase.want})
			if exits != 0 || result.Ended["x"].State != testCase.state {
				t.Fatalf("exits = %d, Ended = %#v", exits, result.Ended)
			}
		})
	}
}

func TestLegacyBlockedResetsTheRestLikeWorking(t *testing.T) {
	seats := newFakeSeats(map[string][]fakeStep{
		"x": {idleFor(1, "a"), blocked(), idleFor(1, "a"), idleFor(2, "a"), gone()},
	})
	lines, _, err := runFleet(t, namedFleet(seats, "x"), WatchOptions{})
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	assertLines(t, lines, []string{"IDLE x idle_seconds=1", "IDLE x idle_seconds=1", "EXIT x"})
}

func TestOnceEndsEachTargetAtItsOwnIdle(t *testing.T) {
	for _, transitions := range []bool{false, true} {
		t.Run(fmt.Sprintf("transitions=%v", transitions), func(t *testing.T) {
			seats := newFakeSeats(map[string][]fakeStep{
				"a": {working("a"), idleFor(2, "b")},
				"b": {working("a"), working("a"), working("a"), idleFor(4, "b")},
			})
			var idled []string
			lines, result, err := runFleet(t, namedFleet(seats, "a", "b"), WatchOptions{
				Once:        true,
				Transitions: transitions,
				OnIdle:      func(status Status) error { idled = append(idled, status.Name); return nil },
			})
			if err != nil {
				t.Fatalf("Watch() error = %v", err)
			}
			want := []string{"IDLE a idle_seconds=2", "IDLE b idle_seconds=4"}
			if transitions {
				want = []string{"SEEN a working", "SEEN b working", "IDLE a idle_seconds=2", "IDLE b idle_seconds=4"}
			}
			assertLines(t, lines, want)
			if !reflect.DeepEqual(idled, []string{"a", "b"}) {
				t.Fatalf("OnIdle seats = %v, want [a b]", idled)
			}
			if seats.inspected("a") != 2 || !result.Ended["a"].Alive() || !result.Ended["b"].Alive() {
				t.Fatalf("a was inspected %d times after it ended, want 2; Ended = %#v",
					seats.inspected("a"), result.Ended)
			}
		})
	}
}

func TestSeenSnapshotIsNotAnIdleLineForOnce(t *testing.T) {
	seats := newFakeSeats(map[string][]fakeStep{"x": {idleFor(7, "a"), working("a"), idleFor(1, "b")}})
	idles := 0
	lines, result, err := runFleet(t, namedFleet(seats, "x"), WatchOptions{
		Once:        true,
		Transitions: true,
		OnIdle:      func(Status) error { idles++; return nil },
	})
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	assertLines(t, lines, []string{"SEEN x idle idle_seconds=7", "WORKING x", "IDLE x idle_seconds=1"})
	if idles != 1 || result.Ended["x"].IdleSeconds != 1 {
		t.Fatalf("idles = %d, Ended = %#v", idles, result.Ended)
	}
}

func TestFleetWatchRunsHooksPerTargetAndNeverOnSnapshotsOrErrors(t *testing.T) {
	seats := newFakeSeats(map[string][]fakeStep{
		"idler": {idleFor(1, "a")},
		"quits": {working("a"), gone()},
	})
	seats.resolveErr["broken"] = errors.New("boom")
	var idled, exited []string
	lines, _, err := runFleet(t, namedFleet(seats, "idler", "quits", "broken"), WatchOptions{
		Once:   true,
		OnIdle: func(status Status) error { idled = append(idled, status.Name); return nil },
		OnExit: func(status Status) error { exited = append(exited, status.Name); return nil },
	})
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	assertLines(t, lines, []string{"IDLE idler idle_seconds=1", "ERROR broken boom", "EXIT quits"})
	if !reflect.DeepEqual(idled, []string{"idler"}) || !reflect.DeepEqual(exited, []string{"quits"}) {
		t.Fatalf("idle hooks = %v, exit hooks = %v", idled, exited)
	}
}

func TestFleetWatchWriteFailureIsTheReturnedError(t *testing.T) {
	seats := newFakeSeats(map[string][]fakeStep{"x": {gone()}})
	_, err := FleetWatcher{Targets: []string{"x"}, Resolve: seats.resolve, Inspect: seats.inspect}.
		Watch(context.Background(), WatchOptions{Poll: time.Millisecond}, failingWriter{})
	if err == nil || !strings.Contains(err.Error(), "write event line") {
		t.Fatalf("Watch() error = %v, want the write failure", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("pipe closed") }
