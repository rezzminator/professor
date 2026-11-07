package hookentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// A settled state (idle, blocked) has no later hook to repair it while the chat
// waits on its user. A hook that could not store or send one under host load
// (a held lock, a tmux or Herdr call past its deadline) starts this verb
// detached: it delivers the state once the host answers, until a newer event
// supersedes it or herdrAgentLease passes.
const (
	herdrSettle       = "settle"
	herdrSettleNative = "native"
	herdrSettleTmux   = "tmux"
	// herdrSettleEvent records a native Claude event its hook could not record.
	herdrSettleEvent = "event"
	// herdrSettleUnproved replays a tmux chat's event dropped before the engine proof.
	herdrSettleUnproved = "unproved"
)

// herdrUndelivered marks a failure that left an event's state unstored or
// unsent: the lock, read, store or send a settle process retries. Any other
// failure (a proof, a name, Herdr's own refusal) a retry would only repeat.
type herdrUndelivered struct{ err error }

func (failure herdrUndelivered) Error() string { return failure.err.Error() }

func (failure herdrUndelivered) Unwrap() error { return failure.err }

func undelivered(err error) error {
	var refusal *herdr.Refusal
	if err == nil || errors.As(err, &refusal) {
		return err
	}
	return herdrUndelivered{err: err}
}

func isUndelivered(err error) bool {
	var failure herdrUndelivered
	return errors.As(err, &failure)
}

func settledState(stored herdrStored) bool {
	return stored.State == string(herdr.StateIdle) || stored.State == string(herdr.StateBlocked)
}

// settleNativeLater starts the settle process for a native Claude's settled
// state the activity record holds but this event could not deliver.
func (reporter *herdrReporter) settleNativeLater(session string, state herdrActivity, err error) error {
	if reporter.options.socket != "" {
		return nil
	}
	return reporter.settleRecordedLater(session, state, err)
}

// settleRecordedLater starts the settle process that re-sends the settled
// state the activity record holds to the panes it last reached, which err left
// undelivered.
func (reporter *herdrReporter) settleRecordedLater(session string, state herdrActivity, err error) error {
	if !isUndelivered(err) || !settledState(state.Stored) || len(state.Targets) == 0 || reporter.deps.spawn == nil {
		return nil
	}
	seq := strconv.FormatUint(state.Stored.Seq, 10)
	if err := reporter.deps.spawn(
		[]string{internalCommand, herdrStateCommand, herdrSettle, herdrSettleNative, seq, session},
	); err != nil {
		return fmt.Errorf("start the settle process for the undelivered %s: %w", state.Stored.State, err)
	}
	return nil
}

// settleEventLater starts the settle process for a native Claude event whose
// hook met its session's activity lock held past the deadline: nothing is
// recorded to re-send, so the process records the event once the lock is
// free, at this event's seq. A subagent start is left out: recorded after its
// own stop, it would count an agent no stop ends.
func (reporter *herdrReporter) settleEventLater(payload herdrPayload, err error) error {
	if !isUndelivered(err) || reporter.deps.identity().TMUX != "" || reporter.deps.spawn == nil ||
		!recordsLater(reporter.options.event) {
		return nil
	}
	var stored herdrStored
	if reporter.options.event != herdr.EventAgentStop {
		action := reporter.plan(payload)
		if action == nil {
			return nil
		}
		stored = action.stored
		stored.MainSeq = reporter.fired // the replay is ordered among main-turn events as the hook was
	}
	return reporter.spawnSettle(herdrSettleEvent, stored, payload, string(reporter.options.event))
}

// recordsLater reports whether a settle process records event for a hook that
// could not.
func recordsLater(event herdr.Event) bool {
	switch event {
	case herdr.EventWorking, herdr.EventBlocked, herdr.EventIdle, herdr.EventNotification, herdr.EventAgentStop:
		return true
	}
	return false
}

// settleUnprovedLater starts the settle process for an engine event whose tmux
// session read got no answer before the hook's deadline, ahead of the engine
// proof. The proof walks up from this hook's process, gone by the time tmux
// answers, so the hook hands over the pane and the ancestry the walk reads.
func (reporter *herdrReporter) settleUnprovedLater(ctx context.Context, payload herdrPayload) error {
	if ctx.Err() == nil || !reporter.firedByEngine() || reporter.tmuxPane == "" || reporter.deps.spawn == nil {
		return nil
	}
	action := reporter.plan(payload)
	if action == nil || action.release {
		return nil
	}
	ancestry, err := reporter.hookAncestry()
	if err != nil {
		return fmt.Errorf("start the settle process for the unproved %s: %w", reporter.options.event, err)
	}
	proof, err := json.Marshal(herdrHookProof{Pane: reporter.tmuxPane, Ancestry: ancestry})
	if err != nil {
		return fmt.Errorf("encode the unproved event's engine proof: %w", err)
	}
	return reporter.spawnSettle(herdrSettleUnproved, action.stored, payload,
		string(proof), herdrEngineFlag, string(reporter.options.engine), herdrSocketFlag, reporter.options.socket,
		string(reporter.options.event))
}

// spawnSettle starts `settle {mode} {seq} {stored} {payload} {rest…}` at this
// event's seq. The replay plans nothing from the tool input or prompt: the
// stored state carries the tool.
func (reporter *herdrReporter) spawnSettle(
	mode string,
	stored herdrStored,
	payload herdrPayload,
	rest ...string,
) error {
	state, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("encode the undelivered state: %w", err)
	}
	payload.ToolInput, payload.Prompt = nil, ""
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode the undelivered event's payload: %w", err)
	}
	argv := append([]string{
		internalCommand, herdrStateCommand, herdrSettle, mode,
		strconv.FormatUint(reporter.seq, 10), string(state), string(body),
	}, rest...)
	if err := reporter.deps.spawn(argv); err != nil {
		return fmt.Errorf("start the settle process for the undelivered %s: %w", reporter.options.event, err)
	}
	return nil
}

// herdrHookProof is what a hook hands a settle process for the engine proof
// it could not finish: the chat pane and the processes from the hook up.
type herdrHookProof struct {
	Pane     string             `json:"pane"`
	Ancestry []herdrHookProcess `json:"ancestry"`
}

type herdrHookProcess struct {
	PID     int    `json:"pid"`
	Parent  int    `json:"parent"`
	Command string `json:"command"`
}

// hookAncestry reads the processes paneEngineFired walks, from this hook up.
func (reporter *herdrReporter) hookAncestry() ([]herdrHookProcess, error) {
	if reporter.deps.procInfo == nil || reporter.deps.getpid == nil {
		return nil, errors.New("no process table to hand the engine proof over")
	}
	var ancestry []herdrHookProcess
	pid := reporter.deps.getpid()
	for range herdrMaxProcessDepth {
		parent, command, err := reporter.deps.procInfo(pid)
		if err != nil {
			return nil, fmt.Errorf("walk up from the hook: %w", err)
		}
		ancestry = append(ancestry, herdrHookProcess{PID: pid, Parent: parent, Command: command})
		if parent <= 1 || parent == pid {
			break
		}
		pid = parent
	}
	return ancestry, nil
}

// proveFrom makes the reporter walk the hook's ancestry the proof recorded.
func (reporter *herdrReporter) proveFrom(proof herdrHookProof) {
	reporter.tmuxPane = proof.Pane
	reporter.deps.getpid = func() int { return proof.Ancestry[0].PID }
	reporter.deps.procInfo = func(pid int) (int, string, error) {
		for _, process := range proof.Ancestry {
			if process.PID == pid {
				return process.Parent, process.Command, nil
			}
		}
		return 0, "", fmt.Errorf("pid %d is not in the hook's recorded ancestry", pid)
	}
}

// settleTmuxLater starts the settle process for a settled state a tmux chat's
// event could not store or send; it replays the event at this event's seq.
func (reporter *herdrReporter) settleTmuxLater(payload herdrPayload, err error) error {
	if !isUndelivered(err) || reporter.options.event.FiredByTmux() {
		return nil
	}
	action := reporter.plan(payload)
	if action == nil {
		return nil
	}
	if action.release || !settledState(action.stored) {
		// No settle replays this event, and the stored seq never moved: only
		// the mark keeps an older settled state's replay off a working pane.
		return reporter.markUnsettled(reporter.session)
	}
	if reporter.deps.spawn == nil {
		return nil
	}
	return reporter.spawnSettle(herdrSettleTmux, action.stored, payload,
		herdrEngineFlag, string(reporter.options.engine), herdrSocketFlag, reporter.options.socket,
		herdrSessionFlag, reporter.session, string(reporter.options.event))
}

// unsettledPath holds the seq of the newest event of a tmux chat session that
// could not store its own state, beside the session's lock.
func (reporter *herdrReporter) unsettledPath(session string) string {
	return filepath.Join(reporter.deps.lockDir, "pfm-herdr-settle", session)
}

// markUnsettled records this event's seq as the session's newest undelivered
// one, unless a newer one is already recorded.
func (reporter *herdrReporter) markUnsettled(session string) error {
	if reporter.deps.lockDir == "" || session != filepath.Base(session) || session == "." || session == ".." {
		return nil
	}
	// An unreadable mark is replaced: this event's seq is the one known newest.
	if newest, err := reporter.unsettled(session); err == nil && newest >= reporter.seq {
		return nil
	}
	path := reporter.unsettledPath(session)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mark the undelivered event of %s: %w", session, err)
	}
	if err := atomicfile.Write(path, []byte(strconv.FormatUint(reporter.seq, 10)), 0o600); err != nil {
		return fmt.Errorf("mark the undelivered event of %s: %w", session, err)
	}
	return nil
}

// unsettled reads the session's newest undelivered event seq; none is 0.
func (reporter *herdrReporter) unsettled(session string) (uint64, error) {
	path := reporter.unsettledPath(session)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read the undelivered event mark %s: %w", path, err)
	}
	seq, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("decode the undelivered event mark %s: %w", path, err)
	}
	return seq, nil
}

func settleHerdrState(args []string, stderr io.Writer, deps herdrDeps) int {
	return settleHerdrStateWithWait(args, stderr, deps, func() { <-clock.Real.After(time.Second) })
}

func settleHerdrStateWithWait(args []string, stderr io.Writer, deps herdrDeps, wait func()) int {
	reporter := &herdrReporter{
		deps:    deps,
		stderr:  stderr,
		options: herdrOptions{engine: pfmengine.Claude, event: herdr.EventIdle},
	}
	var attempt func() (bool, error)
	switch {
	case len(args) == 3 && args[0] == herdrSettleNative && args[2] != "":
		seq, err := strconv.ParseUint(args[1], 10, 64)
		if err != nil {
			reporter.warn("pfm internal herdr-state settle: invalid seq %q: %v", args[1], err)
			return 1
		}
		reporter.session = args[2]
		attempt = func() (bool, error) { return reporter.settleActivity(args[2], seq) }
	case len(args) > 4 && args[0] == herdrSettleTmux:
		seq, stored, payload, ok := reporter.parseSettled(args[1:4])
		if !ok {
			return 1
		}
		options, err := parseHerdrOptions(args[4:])
		if err != nil || options.socket == "" || options.session == "" {
			reporter.warn("pfm internal herdr-state settle: %v, want --socket and --session", err)
			return 1
		}
		reporter.options, reporter.session, reporter.seq = options, options.session, seq
		reporter.activityAction = &herdrAction{stored: stored}
		attempt = func() (bool, error) { return reporter.settleTmux(payload) }
	case len(args) == 5 && args[0] == herdrSettleEvent:
		seq, stored, payload, ok := reporter.parseSettled(args[1:4])
		if !ok {
			return 1
		}
		if payload.SessionID == "" || !recordsLater(herdr.Event(args[4])) {
			reporter.warn("pfm internal herdr-state settle event: session %q, event %q: want a session id "+
				"and an event a settle records", payload.SessionID, args[4])
			return 1
		}
		reporter.options.event, reporter.session, reporter.seq = herdr.Event(args[4]), payload.SessionID, seq
		reporter.fired = stored.MainSeq
		if reporter.options.event != herdr.EventAgentStop {
			reporter.activityAction = &herdrAction{stored: stored}
		}
		recorded := false
		attempt = func() (bool, error) {
			if !recorded {
				if err := reporter.recordEvent(payload); err != nil {
					return reporter.settled(err)
				}
				recorded = true
			}
			return reporter.settleActivity(payload.SessionID, reporter.seq)
		}
	case len(args) > 5 && args[0] == herdrSettleUnproved:
		seq, stored, payload, ok := reporter.parseSettled(args[1:4])
		if !ok {
			return 1
		}
		var proof herdrHookProof
		if err := json.Unmarshal([]byte(args[4]), &proof); err != nil || len(proof.Ancestry) == 0 {
			reporter.warn("pfm internal herdr-state settle: decode the engine proof %q: %v, want an ancestry",
				args[4], err)
			return 1
		}
		options, err := parseHerdrOptions(args[5:])
		if err != nil || options.socket == "" || options.session != "" {
			reporter.warn("pfm internal herdr-state settle unproved: options %q: %v, want --socket and no --session",
				args[5:], err)
			return 1
		}
		reporter.options, reporter.seq = options, seq
		reporter.activityAction = &herdrAction{stored: stored}
		reporter.proveFrom(proof)
		attempt = func() (bool, error) { return reporter.settleUnproved(payload) }
	default:
		reporter.warn("usage: pfm internal herdr-state settle native {seq} {session id}\n" +
			"       pfm internal herdr-state settle tmux {seq} {stored} {payload} " +
			"--engine {engine} --socket {tmux socket} --session {tmux session} {event}\n" +
			"       pfm internal herdr-state settle event {seq} {stored} {payload} {event}\n" +
			"       pfm internal herdr-state settle unproved {seq} {stored} {payload} {proof} " +
			"--engine {engine} --socket {tmux socket} {event}")
		return 1
	}
	until := deps.now().Add(herdrAgentLease)
	return reporter.retry("pfm internal herdr-state settle", func() (bool, error) {
		if deps.now().After(until) {
			reporter.warn("pfm internal herdr-state settle: undelivered after %s, the next event reports the pane",
				herdrAgentLease)
			return true, nil
		}
		return attempt()
	}, wait)
}

// retry runs attempt each wait until it reports done without an error, warning
// each new failure once.
func (reporter *herdrReporter) retry(what string, attempt func() (bool, error), wait func()) int {
	lastError := ""
	for {
		done, err := attempt()
		if err != nil {
			if err.Error() != lastError {
				reporter.warn("%s (retrying): %v", what, err)
				lastError = err.Error()
			}
		} else {
			lastError = ""
			if done {
				return 0
			}
		}
		wait()
	}
}

// settleActivity re-sends the native Claude's recorded state to the panes it
// last reached, unless a newer event has recorded its own.
func (reporter *herdrReporter) settleActivity(session string, seq uint64) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), herdrDeadline)
	defer cancel()
	unlock, err := reporter.lock(ctx, reporter.activityKey(session))
	if err != nil {
		return reporter.settled(err)
	}
	defer unlock()
	state, err := reporter.readActivity(session)
	if err != nil {
		return reporter.settled(err)
	}
	if state.Stored.Seq != seq {
		return true, nil
	}
	bridge, err := reporter.lockBridge(ctx)
	if err != nil {
		return reporter.settled(err)
	}
	defer bridge()
	return reporter.settled(reporter.resendActivity(ctx, state))
}

// settleTmux replays the undelivered event at its own seq: the stored state
// fences it, so a newer event's state stays.
func (reporter *herdrReporter) settleTmux(payload herdrPayload) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), herdrDeadline)
	defer cancel()
	session := reporter.options.session
	unlock, err := reporter.lock(ctx, session)
	if err != nil {
		return reporter.settled(err)
	}
	defer unlock()
	newest, err := reporter.unsettled(session)
	if err != nil {
		return reporter.settled(err)
	}
	if newest > reporter.seq {
		return true, nil
	}
	state, err := reporter.readState(ctx, session)
	if err != nil {
		return reporter.settled(undelivered(err))
	}
	reporter.themeUnlessHeld(ctx, reporter.themeOn(), payload, state)
	return reporter.settled(reporter.event(ctx, session, state, payload))
}

// parseSettled decodes the {seq} {stored} {payload} a hook handed its settle
// process, warning what it could not.
func (reporter *herdrReporter) parseSettled(args []string) (uint64, herdrStored, herdrPayload, bool) {
	var stored herdrStored
	var payload herdrPayload
	seq, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		reporter.warn("pfm internal herdr-state settle: invalid seq %q: %v", args[0], err)
		return 0, stored, payload, false
	}
	if err := errors.Join(
		json.Unmarshal([]byte(args[1]), &stored),
		json.Unmarshal([]byte(args[2]), &payload),
	); err != nil {
		reporter.warn("pfm internal herdr-state settle: decode the undelivered event: %v", err)
		return 0, stored, payload, false
	}
	return seq, stored, payload, true
}

// recordEvent records, under the activity lock, the event its hook could not;
// nothing is sent from here, so no pane proof is needed. A newer event
// supersedes it as it would the hook.
func (reporter *herdrReporter) recordEvent(payload herdrPayload) error {
	ctx, cancel := context.WithTimeout(context.Background(), herdrDeadline)
	defer cancel()
	unlock, err := reporter.lock(ctx, reporter.activityKey(payload.SessionID))
	if err != nil {
		return err
	}
	defer unlock()
	return reporter.recordActivity(ctx, payload, func(context.Context, herdrPayload) error { return nil })
}

// settleUnproved reads the chat session the hook could not, proves the engine
// against the hook's recorded ancestry, then replays the event as settleTmux.
func (reporter *herdrReporter) settleUnproved(payload herdrPayload) (bool, error) {
	if reporter.options.session == "" {
		ctx, cancel := context.WithTimeout(context.Background(), herdrDeadline)
		defer cancel()
		session, panePID, err := reporter.tmuxSession(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return false, err
			}
			return reporter.settled(err)
		}
		if ok, err := reporter.paneEngineFired(panePID); err != nil || !ok {
			return reporter.settled(err)
		}
		reporter.options.session, reporter.session = session, session
	}
	return reporter.settleTmux(payload)
}

// settled is done once nothing is left undelivered; any other failure is
// warned once, since a retry would only repeat it.
func (reporter *herdrReporter) settled(err error) (bool, error) {
	if isUndelivered(err) {
		return false, err
	}
	if err != nil {
		reporter.warn("pfm internal herdr-state settle: stopped, a retry would repeat: %v", err)
	}
	return true, nil
}
