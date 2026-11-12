package hookentry

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// nativeClaude is a Claude hook in a Herdr pane outside tmux: only a proved
// native seat (nativeSeat) may report there.
func (reporter *herdrReporter) nativeClaude() bool {
	return reporter.options.engine == pfmengine.Claude && reporter.deps.identity().TMUX == "" &&
		reporter.options.socket == "" && reporter.processTarget().ok()
}

// Native=false was omitted historically. Positive tmux proof is the complete
// owner protocol (socket, session, numeric client PID) AND a stable terminal,
// not a missing boolean or the activity record's global socket alone.
func (state herdrActivity) tmuxTarget(saved herdrActivityTarget) bool {
	if saved.Native || saved.Terminal == "" || state.TmuxSocket == "" || state.TmuxSession == "" {
		return false
	}
	parts := strings.Split(saved.Owner, "\x00")
	if len(parts) != 3 || parts[0] != state.TmuxSocket || parts[1] != state.TmuxSession {
		return false
	}
	pid, err := strconv.Atoi(parts[2])
	return err == nil && pid > 1
}

// herdrRetirementPending means replay removal is durable, but wire cleanup must retry.
// Healthy targets may still advance; callers preserve the visible error.
type herdrRetirementPending struct{ error }

// Caller holds the activity lock across retirement AND release. Old executables
// take the same lock and reread Targets, so cannot replay a removed target after
// release. An in-flight old send completes before we can acquire that lock.
// Pending releases live outside Targets, invisible to the old worker but
// retryable by a new hook/watch after any transport failure. path is the
// activity record state was read from.
func (reporter *herdrReporter) retireNativeTargets(ctx context.Context, path string, state *herdrActivity) error {
	var kept []herdrActivityTarget
	for _, saved := range state.Targets {
		// A native target under pfm's own source is a pane of an engine Herdr does
		// not detect, which this build reports to. One under herdr:claude is a pane Herdr detects, which
		// its own screen detection owns now; one without a source predates native
		// state reporting. Both are retired.
		if state.tmuxTarget(saved) || (saved.Native && saved.Source == herdr.Source) {
			kept = append(kept, saved)
		} else {
			state.RetiredTargets = append(state.RetiredTargets, saved)
		}
	}
	if len(state.RetiredTargets) == 0 {
		return nil
	}
	state.Targets = kept
	if len(kept) == 0 && state.TmuxSocket == "" {
		state.Stored.Agents = nil
		state.Stored.MainStoppedAt = 0
	}
	if err := reporter.writeActivityFile(path, *state); err != nil {
		return err
	}
	unlock, err := reporter.lockBridge(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var errs []error
	var pending []herdrActivityTarget
	for _, saved := range state.RetiredTargets {
		if err := reporter.releaseNativeTarget(ctx, saved, state.Stored.SessionID); err != nil {
			errs = append(errs, err)
			pending = append(pending, saved)
		}
	}
	state.RetiredTargets = pending
	if err := reporter.writeActivityFile(path, *state); err != nil {
		return errors.Join(append(errs, err)...)
	}
	if err := errors.Join(errs...); err != nil {
		return herdrRetirementPending{err}
	}
	return nil
}

// releaseNativeTarget releases pfm's report on one retired pane under the
// source it went out under; a pfm one only while no other owner took the pane
// since. Releasing
// herdr:claude may clear the restore record Herdr keeps under it, so the session
// goes back once, as Herdr's own Claude integration reports it.
func (reporter *herdrReporter) releaseNativeTarget(
	ctx context.Context,
	saved herdrActivityTarget,
	session string,
) error {
	target := herdrTarget{socket: saved.Socket, pane: saved.Pane, terminal: saved.Terminal}
	owner, err := reporter.paneOwner(target)
	if err != nil {
		return err
	}
	// A newer report by the same owner went out under pfm's source: a pfm release
	// would clear it, a herdr:claude one cannot. No pfm build reports under
	// herdr:claude anymore, so its release goes out whoever holds the pane now;
	// only the session re-send is the old owner's alone.
	sameOwner := owner.Client == saved.Owner
	if saved.Source != herdr.ClaudeSource && (!sameOwner || owner.Seq > saved.Seq) {
		return nil
	}
	if target.terminal != "" {
		client, _, err := reporter.currentPane(
			ctx,
			"",
			herdrClient{Pane: target.pane, Socket: target.socket, Terminal: target.terminal},
			false,
		)
		if isPaneGone(err) || herdr.SocketGone(err) {
			return nil
		}
		if err != nil {
			return err
		}
		target.pane = client.Pane
	}
	agent := herdr.Label(pfmengine.Claude)
	// Old watchdogs send saved.Seq+1; the release must be strictly newer. Under
	// herdr:claude an older build also sent session reports past saved.Seq, so that
	// release takes this run's clock.
	seq := saved.Seq + 2
	if saved.Source == herdr.ClaudeSource {
		seq = max(reporter.nextSent(), seq)
	}
	release, err := herdr.Release{Pane: target.pane, Source: saved.Source, Agent: agent, Seq: seq}.Line()
	if err != nil {
		return err
	}
	lines := [][]byte{release}
	if session != "" && saved.Source == herdr.ClaudeSource && sameOwner {
		report, err := herdr.SessionReport{
			Pane: target.pane, Source: herdr.ClaudeSource, Agent: agent, SessionID: session,
			Seq: max(reporter.nextSent(), seq+1),
		}.Line()
		if err != nil {
			return err
		}
		lines = append(lines, report)
	}
	// A Herdr server that is gone holds no report.
	if err := herdr.Send(ctx, target.socket, lines...); !herdr.SocketGone(err) {
		return releaseSettled(err)
	}
	return nil
}

// releaseSeenPane releases each report pfm left under its own source on the
// seat's pane while Herdr did not see Claude there (left by a build that found
// another process in front of Claude): Herdr sees Claude now, and a
// reported state outranks its screen detection until released. A release that
// fails keeps its target for the next hook, never for the settle process.
func (reporter *herdrReporter) releaseSeenPane(ctx context.Context, seat fleetdb.Seat) error {
	if reporter.activity == nil {
		return nil
	}
	var kept []herdrActivityTarget
	var errs []error
	for _, saved := range reporter.activity.Targets {
		samePane := saved.Pane == seat.HerdrPane || (saved.Terminal != "" && saved.Terminal == seat.HerdrTerminalID)
		if !saved.Native || saved.Socket != seat.HerdrSocket || !samePane {
			kept = append(kept, saved)
			continue
		}
		// Strictly newer than the report and any re-send of it (saved.Seq+1).
		release := herdr.Release{
			Pane: seat.HerdrPane, Source: saved.Source, Agent: herdr.Label(pfmengine.Claude),
			Seq: max(reporter.nextSent(), saved.Seq+2),
		}
		line, err := release.Line()
		if err == nil {
			err = releaseSettled(herdr.Send(ctx, seat.HerdrSocket, line))
		}
		// A Herdr server that is gone holds no report.
		if err != nil && !herdr.SocketGone(err) {
			kept = append(kept, saved)
			errs = append(errs, fmt.Errorf("release pfm's report on pane %s of %s, where Herdr now sees Claude: %w",
				seat.HerdrPane, seat.HerdrSocket, err))
		}
	}
	reporter.activity.Targets = kept
	return errors.Join(errs...)
}

// releaseSettled turns Herdr's pane-not-found for a release (the pane closed
// since it was resolved) into success; every other failure remains visible.
func releaseSettled(err error) error {
	if isHerdrNotFound(err) {
		return nil
	}
	return err
}

func isHerdrNotFound(err error) bool {
	var refusal *herdr.Refusal
	return errors.As(err, &refusal) && (refusal.Code == herdrPaneNotFound || refusal.Code == herdrNotFound)
}
