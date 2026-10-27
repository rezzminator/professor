package hookentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

func (reporter *herdrReporter) nativeClaude() bool {
	return reporter.options.engine == pfmengine.Claude && reporter.deps.getenv("TMUX") == "" &&
		reporter.options.socket == "" && reporter.processTarget().ok()
}

func (reporter *herdrReporter) sessionOnly(
	ctx context.Context,
	target herdrTarget,
	payload herdrPayload,
	seq uint64,
) error {
	argv, err := reporter.resumeArgv(payload.SessionID)
	if err != nil {
		return fmt.Errorf("native Claude session %q: resume command: %w", payload.SessionID, err)
	}
	// Session metadata must join Herdr's native Claude identity. The pfm
	// source is released above and cannot persist a state-free restore record.
	report := herdr.SessionReport{
		Pane: target.pane, Source: "herdr:claude", Agent: herdr.Label(pfmengine.Claude), SessionID: payload.SessionID,
		SessionPath: payload.TranscriptPath, StartSource: payload.Source, ResumeArgv: argv, Seq: seq,
	}
	line, err := report.Line()
	if err != nil {
		return err
	}
	// SessionStart can beat Herdr's native process sampler. Retry only that
	// ownership refusal within the hook's existing deadline, retaining argv.
	// Failure stays visible and leaves migration retryable on a later hook.
	for {
		err := herdr.Send(ctx, target.socket, line)
		if err == nil || !herdr.RefusedResume(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("native Claude restore metadata: %w", errors.Join(err, ctx.Err()))
		case <-clock.Real.After(100 * time.Millisecond):
		}
	}
}

// Called under the native report bridge lock after durable supervisor proof.
// A successful release AND session publication are remembered in the existing
// owner record. A partial failure retries both, never masquerades as complete.
func (reporter *herdrReporter) deferNativeClaude(
	ctx context.Context,
	target herdrTarget,
	payload herdrPayload,
	seq uint64,
) error {
	owner, err := reporter.paneOwner(target)
	if err != nil {
		return err
	}
	if owner.NativeDetection {
		if reporter.options.event != herdr.EventStart {
			return nil
		}
		if seq <= owner.Seq {
			seq = owner.Seq + 1
		}
		if err := reporter.sessionOnly(ctx, target, payload, seq); err != nil {
			return err
		}
		return reporter.storeNativeDetection(target, owner, seq)
	}
	if seq <= owner.Seq {
		seq = owner.Seq + 1
	}
	line, err := (herdr.Release{Pane: target.pane, Agent: herdr.Label(pfmengine.Claude), Seq: seq}).Line()
	if err != nil {
		return err
	}
	if err := releaseSettled(herdr.Send(ctx, target.socket, line)); err != nil {
		return err
	}
	// Release may clear the old session reference: restore it immediately even
	// when this upgrade was first observed on a non-start hook.
	if err := reporter.sessionOnly(ctx, target, payload, seq+1); err != nil {
		return err
	}
	return reporter.storeNativeDetection(target, owner, seq+1)
}

func (reporter *herdrReporter) storeNativeDetection(target herdrTarget, owner herdrPaneOwner, seq uint64) error {
	owner.Seq, owner.NativeDetection = seq, true
	data, err := json.Marshal(owner)
	if err != nil {
		return fmt.Errorf("encode native detection ownership: %w", err)
	}
	if err := atomicfile.Write(reporter.paneOwnerPath(target), data, 0o600); err != nil {
		return fmt.Errorf("store native detection ownership: %w", err)
	}
	return nil
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
// retryable by a new hook/watch after any transport failure.
func (reporter *herdrReporter) retireNativeTargets(ctx context.Context, session string, state *herdrActivity) error {
	var kept []herdrActivityTarget
	for _, saved := range state.Targets {
		if state.tmuxTarget(saved) {
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
	if err := reporter.writeActivity(session, *state); err != nil {
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
	if err := reporter.writeActivity(session, *state); err != nil {
		return errors.Join(append(errs, err)...)
	}
	if err := errors.Join(errs...); err != nil {
		return herdrRetirementPending{err}
	}
	return nil
}

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
	if owner.Client != saved.Owner || owner.Seq > saved.Seq {
		return nil
	}
	if target.terminal != "" {
		client, _, err := reporter.currentPane(
			ctx,
			"",
			herdrClient{Pane: target.pane, Socket: target.socket, Terminal: target.terminal},
			false,
		)
		if isPaneGone(err) {
			return nil
		}
		if err != nil {
			return err
		}
		target.pane = client.Pane
	}
	// Old watchdogs send saved.Seq+1; the release must be strictly newer.
	line, err := (herdr.Release{Pane: target.pane, Agent: herdr.Label(pfmengine.Claude), Seq: saved.Seq + 2}).Line()
	if err != nil {
		return err
	}
	if err := releaseSettled(herdr.Send(ctx, target.socket, line)); err != nil {
		return err
	}
	if session == "" {
		return nil
	}
	before := reporter.nativeSeatReport
	reporter.nativeSeatReport = true
	defer func() { reporter.nativeSeatReport = before }()
	return reporter.sessionOnly(ctx, target, herdrPayload{SessionID: session}, saved.Seq+3)
}
