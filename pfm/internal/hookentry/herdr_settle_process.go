package hookentry

import (
	"encoding/json"
	"io"
	"strconv"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// The settle process (`pfm internal herdr-state settle {mode} …`, started
// detached by a hook whose state the host's load left undelivered) parses the
// mode its hook handed it and retries the mode's attempt (herdr_settle.go). It
// waits herdrSettleFirstWait after a failed attempt, doubling to
// herdrSettleMaxWait, so a loaded host is not asked again every second, and
// gives up after herdrSettleAttempts attempts or herdrSettleFor, whichever
// comes first.
const (
	herdrSettleFirstWait = time.Second
	herdrSettleMaxWait   = 30 * time.Second
	herdrSettleAttempts  = 120
	herdrSettleFor       = time.Hour
)

func settleHerdrState(args []string, stderr io.Writer, deps herdrDeps) int {
	delay := herdrSettleFirstWait
	return settleHerdrStateWithWait(args, stderr, deps, func() {
		<-clock.Real.After(delay)
		delay = min(2*delay, herdrSettleMaxWait)
	})
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
		reporter.session, reporter.seq = args[2], seq
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
	// resendActivity moves reporter.seq to each target's; the give-up names the event's.
	seq, started, attempts := reporter.seq, deps.now(), 0
	return reporter.retry("pfm internal herdr-state settle", func() (bool, error) {
		if spent := deps.now().Sub(started); attempts >= herdrSettleAttempts || spent >= herdrSettleFor {
			reporter.warn("pfm internal herdr-state settle: gave up after %d attempts over %s: the state of seq %d "+
				"stays undelivered, and the pane keeps its last delivered state until the session's next event",
				attempts, spent.Round(time.Second), seq)
			return true, nil
		}
		attempts++
		// Each attempt is its own transaction: a pane list an earlier attempt
		// failed to read under load is never served to a later one.
		reporter.panes, reporter.verifiedClients = nil, nil
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
