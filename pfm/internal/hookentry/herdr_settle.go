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
	if !isUndelivered(err) || reporter.options.socket != "" || !settledState(state.Stored) ||
		len(state.Targets) == 0 || reporter.deps.spawn == nil {
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
	stored, err := json.Marshal(action.stored)
	if err != nil {
		return fmt.Errorf("encode the undelivered state: %w", err)
	}
	// The replay plans nothing from the tool input or prompt: the stored state carries the tool.
	payload.ToolInput, payload.Prompt = nil, ""
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode the undelivered event's payload: %w", err)
	}
	argv := []string{
		internalCommand, herdrStateCommand, herdrSettle, herdrSettleTmux,
		strconv.FormatUint(reporter.seq, 10), string(stored), string(body),
		herdrEngineFlag, string(reporter.options.engine), herdrSocketFlag, reporter.options.socket,
		herdrSessionFlag, reporter.session, string(reporter.options.event),
	}
	if err := reporter.deps.spawn(argv); err != nil {
		return fmt.Errorf("start the settle process for the undelivered %s: %w", action.stored.State, err)
	}
	return nil
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
		seq, err := strconv.ParseUint(args[1], 10, 64)
		if err != nil {
			reporter.warn("pfm internal herdr-state settle: invalid seq %q: %v", args[1], err)
			return 1
		}
		var stored herdrStored
		var payload herdrPayload
		if err := errors.Join(
			json.Unmarshal([]byte(args[2]), &stored),
			json.Unmarshal([]byte(args[3]), &payload),
		); err != nil {
			reporter.warn("pfm internal herdr-state settle: decode the undelivered event: %v", err)
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
	default:
		reporter.warn("usage: pfm internal herdr-state settle native {seq} {session id}\n" +
			"       pfm internal herdr-state settle tmux {seq} {stored} {payload} " +
			"--engine {engine} --socket {tmux socket} --session {tmux session} {event}")
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
