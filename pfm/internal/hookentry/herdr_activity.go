package hookentry

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// A missing SubagentStop cannot hold a pane forever. The lease starts at the
// main Stop (not the agent's birth), and never extends on an idle notification.
const (
	herdrAgentLease  = time.Hour
	herdrAgentsWatch = "agents-watch"
)

// Native seats have no tmux StoredOption. Keep the same stored-state vocabulary
// on disk for all Claude transports, serialized by the existing session lock
// primitive in a separate namespace, before taking any seat/bridge locks.
type herdrActivity struct {
	Stored         herdrStored           `json:"stored"`
	Targets        []herdrActivityTarget `json:"targets,omitempty"`
	RetiredTargets []herdrActivityTarget `json:"retired_targets,omitempty"`
	TmuxSocket     string                `json:"tmux_socket,omitempty"`
	TmuxSession    string                `json:"tmux_session,omitempty"`
}

type herdrActivityTarget struct {
	Pane     string `json:"pane"`
	Socket   string `json:"socket"`
	Terminal string `json:"terminal,omitempty"`
	Owner    string `json:"owner,omitempty"`
	Seq      uint64 `json:"seq"`
	Native   bool   `json:"native,omitempty"`
	// Source is the Herdr source the target's state was reported under: pfm's
	// own, or herdr:claude from an older build (retireNativeTargets releases it).
	Source string `json:"source,omitempty"`
}

// runActivity holds the activity lock across the complete report, so a delayed
// asynchronous tool hook cannot overwrite Stop or a synchronous agent lifecycle.
func (reporter *herdrReporter) runActivity(ctx context.Context, payload herdrPayload) error {
	if reporter.options.engine != pfmengine.Claude || reporter.options.event.FiredByTmux() ||
		payload.SessionID == "" || reporter.deps.lockDir == "" ||
		(reporter.deps.identity().TMUX == "" && !reporter.processTarget().ok()) ||
		(reporter.deps.identity().TMUX != "" && !reporter.hostHasHerdr()) {
		if reporter.options.event == herdr.EventAgentStart || reporter.options.event == herdr.EventAgentStop ||
			payload.AgentID != "" {
			// Without durable membership, an agent stop cannot establish main-turn activity,
			// and a sub-agent's tool event has no stored block to answer.
			return nil
		}
		return reporter.run(ctx, payload)
	}
	if subagentToolEvent(reporter.options.engine, reporter.options.event, payload) &&
		reporter.options.event == herdr.EventWorking {
		// A sub-agent's tool report counts only as the answer to its own stored
		// block: one with none to answer takes no lock (recordActivity checks again under it).
		state, err := reporter.readActivity(payload.SessionID)
		if err != nil {
			return err
		}
		if !answersBlock(payload, state.Stored) {
			return nil
		}
	}
	unlock, err := reporter.lock(ctx, reporter.activityKey(payload.SessionID))
	if err != nil {
		return errors.Join(err, reporter.settleEventLater(payload, err))
	}
	defer unlock()
	return reporter.recordActivity(ctx, payload, reporter.run)
}

// recordActivity is the report under the held activity lock: it records the
// event and delivers it through deliver.
func (reporter *herdrReporter) recordActivity(
	ctx context.Context,
	payload herdrPayload,
	deliver func(context.Context, herdrPayload) error,
) (returnErr error) {
	state, err := reporter.readActivity(payload.SessionID)
	if err != nil {
		return err
	}
	if err := reporter.retireNativeTargets(ctx, reporter.activityPath(payload.SessionID), &state); err != nil {
		var pending herdrRetirementPending
		if !errors.As(err, &pending) {
			return err
		}
		defer func() { returnErr = errors.Join(returnErr, err) }()
	}
	agentEvent := reporter.options.event == herdr.EventAgentStart || reporter.options.event == herdr.EventAgentStop
	mainEvent := !agentEvent && payload.AgentID == ""
	// Counts commute by agent id, unlike main-turn reports: do not drop a delayed
	// lifecycle event just because a newer hook acquired the lock first. For the
	// same reason a main-turn event is stale only behind a newer main-turn one:
	// a sub-agent stopping as an Esc aborts it never outranks the Esc's idle.
	fired := cmp.Or(reporter.fired, reporter.seq)
	switch {
	case mainEvent && state.Stored.MainSeq != 0:
		if state.Stored.MainSeq > fired {
			return nil
		}
	case state.Stored.Seq > reporter.seq && !agentEvent:
		return nil
	}
	if payload.AgentID != "" && reporter.options.event == herdr.EventWorking && !answersBlock(payload, state.Stored) {
		return nil
	}
	mainSeq := state.Stored.MainSeq
	if mainEvent {
		mainSeq = fired
	}
	if state.Stored.Seq >= reporter.seq {
		reporter.seq = state.Stored.Seq + 1
	}
	wasWaiting := state.Stored.MainStoppedAt != 0 && len(state.Stored.Agents) > 0
	action, err := reporter.planActivity(payload, &state.Stored)
	if err != nil {
		return err
	}
	if action == nil {
		if reporter.options.event == herdr.EventEnd {
			return reporter.removeActivity(payload.SessionID)
		}
		if agentEvent {
			return nil
		}
		return deliver(ctx, payload)
	}
	// planActivity updates the lifecycle fields, while preserving the pane result
	// separately until all its hold rules have been evaluated.
	action.stored.Agents = state.Stored.Agents
	action.stored.MainStoppedAt = state.Stored.MainStoppedAt
	action.stored.Seq = reporter.seq
	action.stored.MainSeq = mainSeq
	state.Stored = action.stored
	reporter.activity = &state
	reporter.activityAction = action
	err = deliver(ctx, payload)
	if reporter.options.socket != "" {
		state.TmuxSocket = reporter.options.socket
		state.TmuxSession = reporter.session
	}
	return errors.Join(
		err,
		reporter.finishActivity(payload.SessionID, state, wasWaiting),
		reporter.endWatch(payload, state.Stored),
		reporter.settleNativeLater(payload.SessionID, state, err),
	)
}

func (reporter *herdrReporter) finishActivity(session string, state herdrActivity, wasWaiting bool) error {
	if reporter.options.event == herdr.EventEnd {
		return reporter.removeActivity(session)
	}
	if err := reporter.writeActivity(session, state); err != nil {
		return err
	}
	// An unproved native Claude (a bare launch) reported nowhere: nothing to settle.
	settles := len(state.Targets) > 0 || state.TmuxSocket != ""
	if state.Stored.MainStoppedAt != 0 && len(state.Stored.Agents) > 0 && reporter.deps.spawn != nil &&
		!wasWaiting && settles {
		// The process reads the durable lease, not a timer owned by this short hook.
		if err := reporter.deps.spawn(
			[]string{
				internalCommand,
				herdrStateCommand,
				herdrAgentsWatch,
				session,
				strconv.FormatInt(state.Stored.MainStoppedAt, 10),
			},
		); err != nil {
			return fmt.Errorf("start Claude agent watchdog: %w", err)
		}
	}
	return nil
}

func (reporter *herdrReporter) planActivity(payload herdrPayload, state *herdrStored) (*herdrAction, error) {
	event := reporter.options.event
	action := reporter.plan(payload)
	switch event {
	case herdr.EventStart:
		if action == nil {
			return nil, nil
		}
		state.Agents = nil
		state.MainStoppedAt = 0
	case herdr.EventEnd:
		state.Agents = nil
		state.MainStoppedAt = 0
	case herdr.EventAgentStart, herdr.EventAgentStop:
		if payload.AgentID == "" {
			return nil, errors.New("claude subagent lifecycle missing agent_id (skipped)")
		}
		expected := "SubagentStart"
		if event == herdr.EventAgentStop {
			expected = "SubagentStop"
		}
		if payload.HookEventName != expected {
			// The hook's own CLI event already names the lifecycle; the payload's name only labels it.
			reporter.warn("pfm internal herdr-state %s: hook_event_name %q, want %s (counted as %s)",
				event, payload.HookEventName, expected, event)
		}
		// An agent no longer counted (its lease lapsed, or its start went unrecorded)
		// still closes the dialog it asked for.
		if event == herdr.EventAgentStop && !state.Agents[payload.AgentID] && !ownsBlock(payload.AgentID, *state) {
			return nil, nil
		}
		if state.Agents == nil {
			state.Agents = map[string]bool{}
		}
		if event == herdr.EventAgentStart {
			state.Agents[payload.AgentID] = true
		} else {
			delete(state.Agents, payload.AgentID)
		}
		next := *state
		next.Engine = herdr.Label(pfmengine.Claude)
		next.SessionID = payload.SessionID
		// A stopped sub-agent's own dialog is closed; another's may still be open.
		closed := event == herdr.EventAgentStop && ownsBlock(payload.AgentID, next)
		if next.State != string(herdr.StateBlocked) || closed {
			next.State = string(herdr.StateWorking)
			next.Message = ""
			next.Tool = ""
		}
		action = &herdrAction{stored: next}
	case herdr.EventIdle:
		if state.MainStoppedAt == 0 {
			state.MainStoppedAt = reporter.deps.now().UnixNano()
		}
	case herdr.EventNotification:
		if payload.NotificationType == idlePromptNotification && state.MainStoppedAt == 0 {
			state.MainStoppedAt = reporter.deps.now().UnixNano()
		}
	case herdr.EventWorking:
		// A main-thread tool event also proves an automatic resume. A sub-agent's
		// reaches here only as the answer to its own block (recordActivity), and
		// proves nothing of the main loop; stale main hooks are rejected by the sequence.
		if action != nil && payload.AgentID == "" {
			state.MainStoppedAt = 0
		}
	}
	if action == nil {
		return nil, nil
	}
	if keepsStored(event, payload, *state) {
		action.stored = *state
	}
	if state.MainStoppedAt != 0 && reporter.deps.now().Sub(time.Unix(0, state.MainStoppedAt)) >= herdrAgentLease {
		state.Agents = nil
	}
	if !action.release && state.MainStoppedAt != 0 && action.stored.State != string(herdr.StateBlocked) {
		action.stored.State = string(herdr.StateIdle)
		action.stored.Message = ""
		if len(state.Agents) > 0 {
			action.stored.State = string(herdr.StateWorking)
			action.stored.Message = fmt.Sprintf("%d agent", len(state.Agents))
			if len(state.Agents) != 1 {
				action.stored.Message += "s"
			}
			action.stored.Message += " running"
		}
	}
	return action, nil
}

// rememberActivityTarget runs inside the transport's bridge transaction. The
// watcher may only settle this exact owner, never a later occupant of the pane.
func (reporter *herdrReporter) rememberActivityTarget(target herdrTarget, seq uint64) error {
	if reporter.activity == nil {
		return nil
	}
	owner, err := reporter.paneOwner(target)
	if err != nil {
		return err
	}
	saved := herdrActivityTarget{
		Pane:     target.pane,
		Socket:   target.socket,
		Terminal: target.terminal,
		Owner:    owner.Client,
		Seq:      seq,
		Native:   reporter.nativeSeatReport,
		Source:   herdr.Source,
	}
	for i, old := range reporter.activity.Targets {
		if old.Socket == saved.Socket && old.Pane == saved.Pane {
			reporter.activity.Targets[i] = saved
			return nil
		}
	}
	reporter.activity.Targets = append(reporter.activity.Targets, saved)
	return nil
}

func watchHerdrAgents(args []string, stderr io.Writer, deps herdrDeps) int {
	return watchHerdrAgentsWithWait(args, stderr, deps, func() { <-clock.Real.After(time.Second) })
}

func watchHerdrAgentsWithWait(args []string, stderr io.Writer, deps herdrDeps, wait func()) int {
	reporter := &herdrReporter{
		deps:    deps,
		stderr:  stderr,
		options: herdrOptions{engine: pfmengine.Claude, event: herdr.EventIdle},
	}
	if len(args) != 2 || args[0] == "" {
		reporter.warn("usage: pfm internal herdr-state agents-watch {session id} {stopped timestamp}")
		return 1
	}
	generation, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || generation < 0 {
		reporter.warn("invalid Claude agent watchdog timestamp %q", args[1])
		return 1
	}
	reporter.session = args[0]
	return reporter.retry("Claude agent watchdog", func() (bool, error) {
		return reporter.expireAgents(args[0], generation)
	}, wait)
}

func (reporter *herdrReporter) expireAgents(session string, generation ...int64) (done bool, returnErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), herdrDeadline)
	defer cancel()
	unlock, err := reporter.lock(ctx, reporter.activityKey(session))
	if err != nil {
		return true, err
	}
	defer unlock()
	state, err := reporter.readActivity(session)
	if err != nil {
		return true, err
	}
	if err := reporter.retireNativeTargets(ctx, reporter.activityPath(session), &state); err != nil {
		var pending herdrRetirementPending
		if !errors.As(err, &pending) {
			return true, err
		}
		defer func() { returnErr = errors.Join(returnErr, err) }()
	}
	stopped := state.Stored.MainStoppedAt
	if stopped == 0 || len(state.Stored.Agents) == 0 || (len(generation) > 0 && generation[0] != stopped) {
		return true, nil
	}
	if reporter.deps.now().Sub(time.Unix(0, stopped)) < herdrAgentLease {
		return false, nil
	}
	state.Stored.Agents = nil
	if state.Stored.State == string(herdr.StateBlocked) {
		return true, reporter.writeActivity(session, state)
	}
	state.Stored.State = string(herdr.StateIdle)
	state.Stored.Message = ""
	if state.TmuxSocket != "" {
		reporter.options.socket = state.TmuxSocket
		tmuxUnlock, err := reporter.lock(ctx, state.TmuxSession)
		if err != nil {
			return true, err
		}
		defer tmuxUnlock()
		stored, err := reporter.readState(ctx, state.TmuxSession)
		if err != nil {
			return true, err
		}
		if stored.SessionID == session && stored.State == string(herdr.StateWorking) {
			stored.State = state.Stored.State
			stored.Message = ""
			stored.Agents = nil
			stored.MainStoppedAt = stopped
			stored.Seq++
			if err := reporter.writeState(ctx, state.TmuxSession, stored); err != nil {
				return true, err
			}
			if reporter.themeOn() {
				reporter.themeState(ctx, herdrPayload{SessionID: session})
			}
		}
	}
	bridge, err := reporter.lockBridge(ctx)
	if err != nil {
		return true, err
	}
	defer bridge()
	resent := reporter.resendActivity(ctx, state)
	if err := reporter.writeActivity(session, state); err != nil {
		// The agents stay recorded: the next attempt expires them again.
		return true, errors.Join(resent, err)
	}
	// The record no longer holds an agent to expire, so a retry of this watch
	// would never re-send: the settle process delivers what Herdr missed.
	return true, errors.Join(resent, reporter.settleRecordedLater(session, state, resent))
}

// resendActivity re-sends the activity's stored state to every pane it last
// reached that no other owner or newer report has taken; the caller holds the
// bridge.
func (reporter *herdrReporter) resendActivity(ctx context.Context, state herdrActivity) error {
	var errs []error
	for _, saved := range state.Targets {
		target := herdrTarget{socket: saved.Socket, pane: saved.Pane, terminal: saved.Terminal}
		owner, err := reporter.paneOwner(target)
		if err != nil {
			errs = append(errs, undelivered(err))
			continue
		}
		if owner.Client != saved.Owner || owner.Seq > saved.Seq {
			continue
		}
		if target.terminal != "" {
			resolved, _, err := reporter.currentPane(
				ctx,
				"",
				herdrClient{Pane: target.pane, Socket: target.socket, Terminal: target.terminal},
				false,
			)
			if err != nil {
				errs = append(errs, undelivered(err))
				continue
			}
			target.pane = resolved.Pane
		}
		// The old event sequence is intentional: even an unowned direct pane cannot
		// replace a more recent reporter's state after this watch was scheduled.
		reporter.seq = saved.Seq
		reporter.nativeSeatReport = saved.Native
		errs = append(errs, undelivered(reporter.send(ctx, target, &herdrAction{stored: state.Stored}, saved.Seq+1)))
	}
	return errors.Join(errs...)
}
