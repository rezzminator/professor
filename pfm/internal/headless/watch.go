package headless

import (
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// WatchOptions controls a blocking watch.
type WatchOptions struct {
	// IdleAfter is how long a chat must sit idle before IDLE is emitted. Zero
	// emits as soon as the transcript says the assistant has spoken.
	IdleAfter time.Duration
	// Poll is the sampling cadence.
	Poll time.Duration
	// OnIdle and OnExit run once, when their event fires: OnIdle on an IDLE
	// line, OnExit on an EXIT or DEAD line — never on SEEN or ERROR.
	OnIdle func(Status) error
	OnExit func(Status) error
	// Once stops a target after its first IDLE line, instead of following the
	// chat until it dies. A SEEN snapshot is not an IDLE line.
	Once bool
	// Transitions streams every state change — WORKING, BLOCKED, IDLE — after
	// one SEEN snapshot per target, so a monitor that expires and is re-armed
	// never hears an event twice. Without it only IDLE, EXIT, DEAD and ERROR
	// are written, exactly as before.
	Transitions bool
}

// Watcher samples one chat. Resolve is re-run every tick rather than cached:
// a seat that dies must be SEEN to die, and a resolver that suddenly finds
// nothing is exactly that event.
type Watcher struct {
	Name    string
	Resolve func(context.Context) (Chat, bool, error)
	// Inspect reads the chat's state; nil is Inspect, the transcript-only rule.
	Inspect func(context.Context, Chat, time.Time) (Status, error)
	Now     func() time.Time
	Clock   clock.Clock
}

// Watch blocks, writing one line per event: IDLE when a chat stops owing an
// answer, EXIT when its server is gone, DEAD when it never existed, ERROR when
// resolving or inspecting it failed. It returns the last status observed; a
// target that ended in ERROR returns the original error after the line is
// written.
//
// The exit lines are the point of the command: a monitor that only ever hears
// about work would treat a crash as a long silence.
func (watcher Watcher) Watch(
	ctx context.Context,
	options WatchOptions,
	out io.Writer,
) (Status, error) {
	run := newFleetRun(options, out, watcher.Inspect, watcher.Now, watcher.Clock)
	run.resolve = func(ctx context.Context, _ string) (Chat, bool, error) {
		return watcher.Resolve(ctx)
	}
	seat := &watchedSeat{name: watcher.Name}
	run.named = []*watchedSeat{seat}
	if err := run.run(ctx); err != nil {
		return seat.status, err
	}
	return seat.status, run.result.Errors[watcher.Name]
}

// FleetWatcher watches many seats in one process. A target is a seat name or a
// shell-style glob; a glob picks up seats born after the watch began.
type FleetWatcher struct {
	// Targets are seat names and globs; a target containing any of `*?[` is a
	// glob (path.Match syntax). A malformed pattern is refused before the first
	// poll with path.ErrBadPattern wrapped.
	Targets []string
	// Resolve finds one named target, re-run every poll.
	Resolve func(ctx context.Context, name string) (Chat, bool, error)
	// List reports every seat, re-run every poll while any glob is live.
	List func(ctx context.Context) ([]Chat, error)
	// Inspect reads a seat's state; nil is Inspect, the transcript-only rule.
	Inspect func(context.Context, Chat, time.Time) (Status, error)
	Now     func() time.Time
	Clock   clock.Clock
}

// ClassifyTarget says whether a watch target is a glob (it contains any of
// `*?[`) rather than a seat name, and refuses a malformed pattern with
// path.ErrBadPattern wrapped.
func ClassifyTarget(target string) (glob bool, err error) {
	if !strings.ContainsAny(target, "*?[") {
		return false, nil
	}
	if _, err := path.Match(target, ""); err != nil {
		return true, fmt.Errorf("watch target %q: %w", target, err)
	}
	return true, nil
}

// ErrorLine is the ERROR line of a target whose resolving or inspecting
// failed: the cause with every run of whitespace collapsed to one space, so
// the line stays one monitor event.
func ErrorLine(name string, cause error) string {
	return "ERROR " + name + " " + strings.Join(strings.Fields(cause.Error()), " ")
}

// WatchResult is what each target of a FleetWatcher ended on.
type WatchResult struct {
	// Ended maps a line name to the status it ended on.
	Ended map[string]Status
	// Errors maps a line name (a seat, or a glob pattern whose listing failed)
	// to the error that ended it.
	Errors map[string]error
}

// Watch writes every target's lines to out and returns once every named target
// has ended and no glob was given; with a glob it runs until ctx ends or every
// glob's listing has failed. The error is only a write failure, a hook failure
// or ctx cancellation: a target's own failure is an ERROR line and an entry in
// WatchResult.Errors, and the other targets carry on.
func (watcher FleetWatcher) Watch(
	ctx context.Context,
	options WatchOptions,
	out io.Writer,
) (WatchResult, error) {
	run := newFleetRun(options, out, watcher.Inspect, watcher.Now, watcher.Clock)
	run.resolve = watcher.Resolve
	run.list = watcher.List
	named := map[string]bool{}
	for _, target := range watcher.Targets {
		if named[target] {
			continue
		}
		named[target] = true
		glob, err := ClassifyTarget(target)
		if err != nil {
			return run.result, err
		}
		if glob {
			run.globs = append(run.globs, &watchedSeat{name: target, pattern: true})
		} else {
			run.named = append(run.named, &watchedSeat{name: target})
		}
	}
	if len(run.named) > 0 && run.resolve == nil {
		return run.result, fmt.Errorf("watch: named targets need a Resolve")
	}
	if len(run.globs) > 0 && run.list == nil {
		return run.result, fmt.Errorf("watch: glob targets need a List")
	}
	err := run.run(ctx)
	return run.result, err
}

// watchedSeat is one target's memory across polls.
type watchedSeat struct {
	name    string
	pattern bool // a glob, not a seat: it only ever ends on a failed listing
	seen    bool
	ended   bool
	status  Status
	// announcedIdle is the legacy rule's one bit: IDLE already written.
	announcedIdle bool
	// announced is the transitions rule's memory: the last state key written.
	announced string
	lastSeen  string // the previous poll's Status.Last, for the missed-turn rule
}

// fleetRun is one watch: the state machine every target shares.
type fleetRun struct {
	options WatchOptions
	out     io.Writer
	resolve func(ctx context.Context, name string) (Chat, bool, error)
	list    func(ctx context.Context) ([]Chat, error)
	inspect func(context.Context, Chat, time.Time) (Status, error)
	now     func() time.Time
	clock   clock.Clock

	named   []*watchedSeat
	globs   []*watchedSeat
	adopted map[string]*watchedSeat
	// claimed holds the last resolved name of every named target, so a glob
	// never streams a seat a named target already does.
	claimed map[string]bool
	result  WatchResult
}

func newFleetRun(
	options WatchOptions,
	out io.Writer,
	inspect func(context.Context, Chat, time.Time) (Status, error),
	now func() time.Time,
	timerClock clock.Clock,
) *fleetRun {
	if inspect == nil {
		inspect = Inspect
	}
	if timerClock == nil {
		timerClock = clock.Real
	}
	if now == nil {
		now = timerClock.Now
	}
	if options.Poll <= 0 {
		options.Poll = time.Second
	}
	return &fleetRun{
		options: options,
		out:     out,
		inspect: inspect,
		now:     now,
		clock:   timerClock,
		adopted: map[string]*watchedSeat{},
		claimed: map[string]bool{},
		result: WatchResult{
			Ended:  map[string]Status{},
			Errors: map[string]error{},
		},
	}
}

func (run *fleetRun) run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := run.pollRound(ctx); err != nil {
			return err
		}
		if run.finished() {
			return nil
		}
		if err := waitForNextPoll(ctx, run.clock, run.options.Poll); err != nil {
			return err
		}
	}
}

// finished is true once nothing is left to watch: every named target ended and
// every glob (if any) ended on a failed listing.
func (run *fleetRun) finished() bool {
	for _, seat := range run.named {
		if !seat.ended {
			return false
		}
	}
	return run.liveGlobs() == 0
}

func (run *fleetRun) liveGlobs() int {
	live := 0
	for _, glob := range run.globs {
		if !glob.ended {
			live++
		}
	}
	return live
}

// pollRound is one poll: every live named target in argument order, then the
// seats the globs adopted, sorted by name.
func (run *fleetRun) pollRound(ctx context.Context) error {
	for _, seat := range run.named {
		if seat.ended {
			continue
		}
		chat, found, err := run.resolve(ctx, seat.name)
		if err != nil {
			if err := run.failSeat(ctx, seat, err); err != nil {
				return err
			}
			continue
		}
		if found {
			run.claimed[chat.Name] = true
		}
		if err := run.advance(ctx, seat, chat, found); err != nil {
			return err
		}
	}
	if run.liveGlobs() == 0 {
		return nil
	}
	chats, err := run.list(ctx)
	if err != nil {
		return run.failGlobs(ctx, err)
	}
	listed := map[string]Chat{}
	for index := range chats {
		chat := &chats[index]
		if previous, dup := listed[chat.Name]; !dup || (!previous.Live && chat.Live) {
			listed[chat.Name] = *chat
		}
	}
	// A seat that ended is let go only once a listing no longer shows it live,
	// so a respawn under the same name is a fresh seat with a fresh SEEN.
	for name, seat := range run.adopted {
		chat, ok := listed[name]
		if seat.ended && (!ok || !chat.Live || !run.globMatches(name)) {
			delete(run.adopted, name)
		}
	}
	for index := range chats {
		chat := &chats[index]
		if !chat.Live || run.adopted[chat.Name] != nil || run.claimed[chat.Name] || !run.globMatches(chat.Name) {
			continue
		}
		run.adopted[chat.Name] = &watchedSeat{name: chat.Name}
	}
	for _, name := range run.liveAdopted() {
		chat, found := listed[name]
		if err := run.advance(ctx, run.adopted[name], chat, found); err != nil {
			return err
		}
	}
	return nil
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

// liveAdopted is the adopted seats still being followed, sorted by name.
func (run *fleetRun) liveAdopted() []string {
	names := make([]string, 0, len(run.adopted))
	for name, seat := range run.adopted {
		if !seat.ended {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// advance reads one target once: found false is a name that resolves to
// nothing, otherwise chat is inspected.
func (run *fleetRun) advance(ctx context.Context, seat *watchedSeat, chat Chat, found bool) error {
	if !found {
		seat.status = Missing(seat.name)
		if run.options.Transitions && !seat.seen {
			return run.snapshot(seat)
		}
		return run.leave(seat, "DEAD")
	}
	status, err := run.inspect(ctx, chat, run.now())
	if err != nil {
		return run.failSeat(ctx, seat, err)
	}
	seat.status = status
	if run.options.Transitions && !seat.seen {
		return run.snapshot(seat)
	}
	if !status.Alive() {
		return run.leave(seat, "EXIT")
	}
	if run.options.Transitions {
		return run.announceTransition(seat)
	}
	return run.announceLegacy(seat)
}

// say writes one event line.
func (run *fleetRun) say(format string, args ...any) error {
	if _, err := fmt.Fprintf(run.out, format+"\n", args...); err != nil {
		return fmt.Errorf("watch: write event line: %w", err)
	}
	return nil
}

// finish ends a seat on its last status.
func (run *fleetRun) finish(seat *watchedSeat) {
	seat.ended = true
	if !seat.pattern {
		run.result.Ended[seat.name] = seat.status
	}
}

// leave writes the EXIT or DEAD line, runs OnExit, and ends the seat.
func (run *fleetRun) leave(seat *watchedSeat, word string) error {
	run.finish(seat)
	if err := run.say("%s %s", word, seat.name); err != nil {
		return err
	}
	if run.options.OnExit != nil {
		return run.options.OnExit(seat.status)
	}
	return nil
}

// failSeat writes the ERROR line, ends the seat and records its error. A
// cancelled context is the watch ending, not the seat failing.
func (run *fleetRun) failSeat(ctx context.Context, seat *watchedSeat, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	run.finish(seat)
	run.result.Errors[seat.name] = cause
	return run.say("%s", ErrorLine(seat.name, cause))
}

// failGlobs is a listing that could not be read: every glob and every seat the
// globs adopted fails with it, since none can be followed any longer.
func (run *fleetRun) failGlobs(ctx context.Context, cause error) error {
	for _, glob := range run.globs {
		if glob.ended {
			continue
		}
		if err := run.failSeat(ctx, glob, cause); err != nil {
			return err
		}
	}
	for _, name := range run.liveAdopted() {
		if err := run.failSeat(ctx, run.adopted[name], cause); err != nil {
			return err
		}
	}
	return nil
}

// idle writes the IDLE line, runs OnIdle, and ends the seat under Once.
func (run *fleetRun) idle(seat *watchedSeat) error {
	status := seat.status
	// A turn ended on an error waits for its human exactly as an answered one
	// does, so it is announced as idle with its kind.
	suffix := ""
	if status.State == StateError {
		suffix = " error=" + status.Error
	}
	if err := run.say("IDLE %s idle_seconds=%d%s", seat.name, status.IdleSeconds, suffix); err != nil {
		return err
	}
	if run.options.OnIdle != nil {
		if err := run.options.OnIdle(status); err != nil {
			return err
		}
	}
	if run.options.Once {
		run.finish(seat)
	}
	return nil
}

func endedTurn(status Status) bool {
	return status.State == StateIdle || status.State == StateError
}

func idleLongEnough(status Status, after time.Duration) bool {
	return time.Duration(status.IdleSeconds)*time.Second >= after
}

// announceLegacy is the original rule: IDLE once per rest, re-armed by any
// alive state that is not a rest.
func (run *fleetRun) announceLegacy(seat *watchedSeat) error {
	status := seat.status
	switch {
	case endedTurn(status) && idleLongEnough(status, run.options.IdleAfter) && !seat.announcedIdle:
		seat.announcedIdle = true
		return run.idle(seat)
	case !endedTurn(status):
		// Back to work: the next idle is a new event worth announcing.
		seat.announcedIdle = false
	}
	return nil
}

// stateKey is what a transition is announced as: a turn that ended is one
// state per error kind, every other state is itself.
func stateKey(status Status) string {
	if endedTurn(status) {
		return StateIdle + ":" + status.Error
	}
	return status.State
}

// snapshot writes the one SEEN line a target gets, on first sight, and seeds
// what later transitions are compared with. A target seen not alive ends here.
func (run *fleetRun) snapshot(seat *watchedSeat) error {
	status := seat.status
	seat.seen = true
	line := fmt.Sprintf("SEEN %s %s", seat.name, status.State)
	if endedTurn(status) {
		line += fmt.Sprintf(" idle_seconds=%d", status.IdleSeconds)
	}
	if status.State == StateError {
		line += " error=" + status.Error
	}
	if err := run.say("%s", line); err != nil {
		return err
	}
	if !status.Alive() {
		run.finish(seat)
		return nil
	}
	seat.announced = stateKey(status)
	seat.lastSeen = status.Last
	return nil
}

// announceTransition writes a line only when the state differs from the last
// one written.
func (run *fleetRun) announceTransition(seat *watchedSeat) error {
	status := seat.status
	previous := seat.lastSeen
	seat.lastSeen = status.Last
	// A turn that began and ended between two polls leaves the seat at rest
	// with a new last answer: say it worked, then judge the rest anew.
	if endedTurn(status) && strings.HasPrefix(seat.announced, StateIdle+":") && status.Last != previous {
		if err := run.say("%s %s", strings.ToUpper(StateWorking), seat.name); err != nil {
			return err
		}
		seat.announced = StateWorking
	}
	key := stateKey(status)
	if endedTurn(status) {
		if !idleLongEnough(status, run.options.IdleAfter) || key == seat.announced {
			return nil
		}
		seat.announced = key
		return run.idle(seat)
	}
	if key == seat.announced {
		return nil
	}
	seat.announced = key
	return run.say("%s %s", strings.ToUpper(status.State), seat.name)
}
