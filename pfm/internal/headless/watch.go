package headless

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// WatchOptions controls a blocking watch.
type WatchOptions struct {
	// IdleAfter is how long a chat must sit idle before IDLE is emitted. Zero
	// emits as soon as the transcript says the assistant has spoken.
	IdleAfter time.Duration
	// Poll is the sampling cadence, floored at MinPoll: zero or anything
	// shorter runs at MinPoll.
	Poll time.Duration
	// OnIdle and OnExit run once, when their event fires: OnIdle on an IDLE
	// line, OnExit on an EXIT or DEAD line — never on SEEN or ERROR.
	OnIdle func(Status) error
	OnExit func(Status) error
	// Once stops a target after its first IDLE line, instead of following the
	// chat until it dies. A SEEN snapshot is not an IDLE line.
	Once bool
	// Transitions streams every state change — WORKING, BLOCKED, QUIET, IDLE — after
	// one SEEN snapshot per target, so a monitor that expires and is re-armed
	// never hears an event twice. Without it only IDLE, EXIT, DEAD and ERROR
	// are written, exactly as before.
	Transitions bool
	// NoWorking omits WORKING event lines under Transitions. SEEN snapshots
	// still name a working chat, and BLOCKED, QUIET, EXIT and ERROR remain.
	NoWorking bool
	// Answer reads the last answer only for the NoWorking monitor's idle
	// events. Failures appear on the event line, never as an empty answer.
	Answer func(context.Context, Chat) (string, error)
	// QuietAfter, under Transitions and when positive, announces a working
	// seat whose transcript has sat unchanged that long as QUIET, once, and
	// WORKING again when the transcript moves: a hung stream, a reconnect
	// loop or an unseen dialog never ends a turn, so IDLE alone misses it.
	QuietAfter time.Duration
}

// MinPoll is the shortest cadence a watch samples at, and its default. A
// sample costs a fleet discovery plus a transcript read per target, and a
// watcher is a background wait for a calling chat that acts on its events in
// turns of its own: polling faster bought no event it could use sooner, only
// the CPU fourteen concurrent watchers burned between them.
const MinPoll = 10 * time.Second

// PollFloor is the cadence a requested poll runs at: MinPoll for anything
// shorter, zero and negative included.
func PollFloor(requested time.Duration) time.Duration {
	if requested < MinPoll {
		return MinPoll
	}
	return requested
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

// UnreadablePolls is how many polls in a row a followed seat's tmux server may
// go unread before the watch says so with an ERROR line. A server that cannot
// be read is no evidence the chat ended: until then the seat's verdict is
// held, and nothing is announced for it.
const UnreadablePolls = 3

// Discovery is one poll's view of the fleet, shared by every target of the
// poll, so a poll costs one discovery however many targets it serves.
type Discovery struct {
	// Resolve finds one named target.
	Resolve func(ctx context.Context, name string) (Chat, bool, error)
	// List reports every seat. Nil when the discovery offers no listing.
	List func(ctx context.Context) ([]Chat, error)
	// Unreadable names each tmux socket whose server this discovery could
	// not read, with why. Asked only after Resolve or List answered.
	Unreadable func() map[string]string
}

// FleetWatcher watches many seats in one process. A target is a seat name or a
// shell-style glob; a glob picks up seats born after the watch began.
//
// A named target is the seat it first resolves to live: the watch follows
// that seat (its socket and pane) through a /clear or a rename, and ends it
// with EXIT when the seat is gone — a newcomer taking the name is a different
// chat, never a continuation. A glob follows every live seat whose name
// matches, each seat on its own, so two seats sharing a name are two seats.
type FleetWatcher struct {
	// Targets are seat names and globs; a target containing any of `*?[` is a
	// glob (path.Match syntax). A malformed pattern is refused before the first
	// poll with path.ErrBadPattern wrapped.
	Targets []string
	// Discover opens each poll's shared discovery. since is the earliest
	// instant a fleet view shared with other watches may have been taken at
	// and still serve this poll: the poll's tick on the shared grid, or the
	// poll's own start when that came first. Nil polls Resolve and List
	// directly.
	Discover func(since time.Time) Discovery
	// Resolve finds one named target, re-run every poll.
	Resolve func(ctx context.Context, name string) (Chat, bool, error)
	// List reports every seat, re-run every poll while any glob is live.
	List func(ctx context.Context) ([]Chat, error)
	// Inspect reads a seat's state; nil is Inspect, the transcript-only rule.
	Inspect func(context.Context, Chat, time.Time) (Status, error)
	// Fingerprint condenses everything a followed seat's listing row rests on
	// into one value read cheaply, without a fleet scan; ok is false when the
	// seat cannot be fingerprinted. While a named target's seat keeps the
	// fingerprint taken before the scan its row came from, the watch reuses
	// that row instead of scanning (and scans anyway every SeatPrintReuse
	// polls). Nil scans every poll.
	Fingerprint func(ctx context.Context, chat Chat) (string, bool)
	Now         func() time.Time
	Clock       clock.Clock
}

// SeatPrintReuse is how many polls in a row a followed seat's row is reused on
// an unchanged fingerprint before the watch scans anyway.
const SeatPrintReuse = 30

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
	run.discover = watcher.Discover
	run.fingerprint = watcher.Fingerprint
	if len(run.named) > 0 && run.resolve == nil && run.discover == nil {
		return run.result, fmt.Errorf("watch: named targets need a Resolve")
	}
	if len(run.globs) > 0 && run.list == nil && run.discover == nil {
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
	chat    Chat
	// announcedIdle is the legacy rule's one bit: IDLE already written.
	announcedIdle bool
	// announced is the transitions rule's memory: the last state key written.
	announced string
	lastSeen  string // the previous poll's Status.Last, for the missed-turn rule
	// bound is the live seat a named target first resolved to, or the seat a
	// glob adopted: what the watch follows from then on.
	bound *Chat
	// unread counts the polls in a row this seat's server could not be read.
	unread int
	// print is the fingerprint taken before the scan the bound row came from,
	// "" when there is none to trust; reused counts the polls since that scan.
	print  string
	reused int
}

// seatPrint is one followed seat's fingerprint this poll.
type seatPrint struct {
	value string
	ok    bool
}

// seatKey is a glob-adopted seat: its name (what the glob matched), and the
// socket and pane that make two seats sharing one name two seats.
type seatKey struct{ name, socket, pane string }

func keyOf(chat Chat) seatKey { return seatKey{name: chat.Name, socket: chat.Socket, pane: chat.Pane} }

// sameSeat says two chats are one seat: one pane on one socket. A chat with
// neither (a test fake, a seat known by name alone) is its name.
func sameSeat(left, right Chat) bool {
	if left.Socket == "" && left.Pane == "" && right.Socket == "" && right.Pane == "" {
		return left.Name == right.Name
	}
	return left.Socket == right.Socket && left.Pane == right.Pane
}

// socketless says a listing row carries neither socket nor pane: its tmux
// server went unread that scan (a probe timed out under load), or its socket
// file was unlinked under a running engine. The seat is then known by its
// conversation alone.
func socketless(chat Chat) bool { return chat.Socket == "" && chat.Pane == "" }

// sameConversation says chat is the seat bound follows when either was listed
// without its socket: one conversation, listed with its socket one scan and
// without it the next, is one seat.
func sameConversation(bound, chat Chat) bool {
	return bound.ID != "" && chat.ID == bound.ID && (socketless(bound) || socketless(chat))
}

// claimKey is what a named target claims so no glob streams its seat twice.
func claimKey(chat Chat) string {
	if chat.Socket == "" && chat.Pane == "" {
		return "name\x00" + chat.Name
	}
	return "seat\x00" + chat.Socket + "\x00" + chat.Pane
}

// fleetRun is one watch: the state machine every target shares.
type fleetRun struct {
	options  WatchOptions
	out      io.Writer
	resolve  func(ctx context.Context, name string) (Chat, bool, error)
	list     func(ctx context.Context) ([]Chat, error)
	discover func(since time.Time) Discovery
	inspect  func(context.Context, Chat, time.Time) (Status, error)
	now      func() time.Time
	clock    clock.Clock
	// fingerprint reads a followed seat's fingerprint (FleetWatcher.Fingerprint).
	fingerprint func(ctx context.Context, chat Chat) (string, bool)

	named   []*watchedSeat
	globs   []*watchedSeat
	adopted map[seatKey]*watchedSeat
	// claimed holds the seat of every named target (claimKey), so a glob
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
	options.Poll = PollFloor(options.Poll)
	return &fleetRun{
		options: options,
		out:     out,
		inspect: inspect,
		now:     now,
		clock:   timerClock,
		adopted: map[seatKey]*watchedSeat{},
		claimed: map[string]bool{},
		result: WatchResult{
			Ended:  map[string]Status{},
			Errors: map[string]error{},
		},
	}
}

func (run *fleetRun) run(ctx context.Context) error {
	since := run.now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := run.pollRound(ctx, since); err != nil {
			return err
		}
		if run.finished() {
			return nil
		}
		now := run.now()
		tick := NextTick(now, run.options.Poll)
		if err := waitForNextPoll(ctx, run.clock, tick.Sub(now)); err != nil {
			return err
		}
		since = tick
		if woke := run.now(); woke.Before(tick) {
			since = woke
		}
	}
}

// NextTick is the first instant after now on poll's grid, the multiples of
// poll since the Unix epoch. Every watch with the same poll samples on the
// same grid, so watches started apart still poll together and one fleet scan
// serves them all; no wait is ever longer than poll.
func NextTick(now time.Time, poll time.Duration) time.Time {
	step := int64(poll)
	return time.Unix(0, (now.UnixNano()/step+1)*step)
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

// advance reads one target once: found false is a name that resolves to
// nothing, otherwise chat is inspected.
func (run *fleetRun) advance(ctx context.Context, seat *watchedSeat, chat Chat, found bool) error {
	seat.unread = 0
	if !found {
		seat.status = Missing(seat.name)
		if run.options.Transitions && !seat.seen {
			return run.snapshot(ctx, seat)
		}
		return run.leave(seat, "DEAD")
	}
	status, err := run.inspect(ctx, chat, run.now())
	if err != nil {
		return run.failSeat(ctx, seat, err)
	}
	seat.chat = chat
	seat.status = status
	if run.options.Transitions && !seat.seen {
		return run.snapshot(ctx, seat)
	}
	if !status.Alive() {
		return run.leave(seat, "EXIT")
	}
	if run.options.Transitions {
		return run.announceTransition(ctx, seat)
	}
	return run.announceLegacy(ctx, seat)
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
	for _, key := range run.liveAdopted() {
		if err := run.failSeat(ctx, run.adopted[key], cause); err != nil {
			return err
		}
	}
	return nil
}

// answerSuffix keeps the monitor's verdict on one line, with explicit read
// failures. The default watch never calls the reader or changes its format.
func (run *fleetRun) answerSuffix(ctx context.Context, seat *watchedSeat) string {
	if !run.options.NoWorking {
		return ""
	}
	if run.options.Answer == nil {
		return ` answer_error="reader not configured"`
	}
	text, err := run.options.Answer(ctx, seat.chat)
	if err != nil {
		return fmt.Sprintf(" answer_error=%q", strings.Join(strings.Fields(err.Error()), " "))
	}
	first, verdict := "", ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		if first == "" {
			first = line
		}
		if strings.HasPrefix(line, "**Verdict:**") {
			verdict = line
			break
		}
	}
	if verdict != "" {
		first = verdict
	}
	if first == "" {
		return ` answer_error="empty answer"`
	}
	const limit = 200
	characters := []rune(first)
	if len(characters) > limit {
		first = string(characters[:limit-1]) + "…"
	}
	return fmt.Sprintf(" answer=%q", first)
}

// idle writes the IDLE line, runs OnIdle, and ends the seat under Once.
func (run *fleetRun) idle(ctx context.Context, seat *watchedSeat) error {
	status := seat.status
	// A turn ended on an error waits for its human exactly as an answered one
	// does, so it is announced as idle with its kind.
	suffix := ""
	if status.State == StateError {
		suffix = " error=" + status.Error
	}
	suffix += run.answerSuffix(ctx, seat)
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
func (run *fleetRun) announceLegacy(ctx context.Context, seat *watchedSeat) error {
	status := seat.status
	switch {
	case endedTurn(status) && idleLongEnough(status, run.options.IdleAfter) && !seat.announcedIdle:
		seat.announcedIdle = true
		return run.idle(ctx, seat)
	case !endedTurn(status):
		// Back to work: the next idle is a new event worth announcing.
		seat.announcedIdle = false
	}
	return nil
}

// stateQuiet is the transition key of a working seat silent for QuietAfter.
// It is the watch's own judgment, never a Status.State.
const stateQuiet = "quiet"

// quiet is a working seat whose transcript has sat unchanged for QuietAfter.
func (run *fleetRun) quiet(status Status) bool {
	after := run.options.QuietAfter
	return run.options.Transitions && after > 0 && status.State == StateWorking &&
		time.Duration(status.QuietSeconds)*time.Second >= after
}

// stateKey is what a transition is announced as: a turn that ended is one
// state per error kind, a working seat silent for QuietAfter is quiet, every
// other state is itself.
func (run *fleetRun) stateKey(status Status) string {
	switch {
	case endedTurn(status):
		return StateIdle + ":" + status.Error
	case run.quiet(status):
		return stateQuiet
	}
	return status.State
}

// snapshot writes the one SEEN line a target gets, on first sight, and seeds
// what later transitions are compared with. A target seen not alive ends here.
func (run *fleetRun) snapshot(ctx context.Context, seat *watchedSeat) error {
	status := seat.status
	seat.seen = true
	line := fmt.Sprintf("SEEN %s %s", seat.name, status.State)
	if endedTurn(status) {
		line += fmt.Sprintf(" idle_seconds=%d", status.IdleSeconds)
	}
	if status.State == StateError {
		line += " error=" + status.Error
	}
	if run.quiet(status) {
		line += fmt.Sprintf(" quiet_seconds=%d", status.QuietSeconds)
	}
	if endedTurn(status) {
		line += run.answerSuffix(ctx, seat)
	}
	if err := run.say("%s", line); err != nil {
		return err
	}
	if !status.Alive() {
		run.finish(seat)
		return nil
	}
	seat.announced = run.stateKey(status)
	seat.lastSeen = status.Last
	return nil
}

// announceTransition writes a line only when the state differs from the last
// one written.
func (run *fleetRun) announceTransition(ctx context.Context, seat *watchedSeat) error {
	status := seat.status
	previous := seat.lastSeen
	seat.lastSeen = status.Last
	// A turn that began and ended between two polls leaves the seat at rest
	// with a new last answer: say it worked, then judge the rest anew.
	if endedTurn(status) && strings.HasPrefix(seat.announced, StateIdle+":") && status.Last != previous {
		if !run.options.NoWorking {
			if err := run.say("%s %s", strings.ToUpper(StateWorking), seat.name); err != nil {
				return err
			}
		}
		seat.announced = StateWorking
	}
	key := run.stateKey(status)
	if endedTurn(status) {
		if !idleLongEnough(status, run.options.IdleAfter) || key == seat.announced {
			return nil
		}
		seat.announced = key
		return run.idle(ctx, seat)
	}
	if key == seat.announced {
		return nil
	}
	seat.announced = key
	if key == stateQuiet {
		return run.say("QUIET %s quiet_seconds=%d", seat.name, status.QuietSeconds)
	}
	if run.options.NoWorking && key == StateWorking {
		return nil
	}
	return run.say("%s %s", strings.ToUpper(status.State), seat.name)
}
