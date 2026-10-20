package hookentry

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
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
	Stored      herdrStored           `json:"stored"`
	Targets     []herdrActivityTarget `json:"targets,omitempty"`
	TmuxSocket  string                `json:"tmux_socket,omitempty"`
	TmuxSession string                `json:"tmux_session,omitempty"`
}

type herdrActivityTarget struct {
	Pane     string `json:"pane"`
	Socket   string `json:"socket"`
	Terminal string `json:"terminal,omitempty"`
	Owner    string `json:"owner,omitempty"`
	Seq      uint64 `json:"seq"`
	Native   bool   `json:"native,omitempty"`
}

func (reporter *herdrReporter) activityKey(session string) string {
	return fmt.Sprintf("activity-%x", sha256.Sum256([]byte(session)))
}

func (reporter *herdrReporter) activityPath(session string) string {
	return filepath.Join(reporter.deps.lockDir, reporter.activityKey(session)+".json")
}

func (reporter *herdrReporter) readActivity(session string) (herdrActivity, error) {
	var state herdrActivity
	file, err := os.OpenFile(reporter.activityPath(session), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("open Claude activity: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return state, fmt.Errorf("inspect Claude activity: %w", err)
	}
	const maxActivityBytes = 1 << 20
	if !info.Mode().IsRegular() || info.Size() > maxActivityBytes {
		return state, fmt.Errorf("claude activity is not a regular file of at most %d bytes", maxActivityBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxActivityBytes+1))
	if err != nil {
		return state, fmt.Errorf("read Claude activity: %w", err)
	}
	if len(data) > maxActivityBytes {
		return state, fmt.Errorf("claude activity exceeds %d bytes", maxActivityBytes)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("decode Claude activity: %w", err)
	}
	return state, nil
}

func (reporter *herdrReporter) writeActivity(session string, state herdrActivity) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode Claude activity: %w", err)
	}
	if err := atomicfile.Write(reporter.activityPath(session), data, 0o600); err != nil {
		return fmt.Errorf("store Claude activity: %w", err)
	}
	return nil
}

// runActivity holds the activity lock across the complete report, so a delayed
// asynchronous tool hook cannot overwrite Stop or a synchronous agent lifecycle.
func (reporter *herdrReporter) runActivity(ctx context.Context, payload herdrPayload) error {
	if reporter.options.engine != pfmengine.Claude || reporter.options.event.FiredByTmux() ||
		payload.SessionID == "" || reporter.deps.lockDir == "" ||
		(reporter.deps.getenv("TMUX") == "" && !reporter.processTarget().ok()) ||
		(reporter.deps.getenv("TMUX") != "" && !reporter.hostHasHerdr()) {
		if reporter.options.event == herdr.EventAgentStart || reporter.options.event == herdr.EventAgentStop {
			// Without durable membership, an agent stop cannot establish main-turn activity.
			return nil
		}
		return reporter.run(ctx, payload)
	}
	unlock, err := reporter.lock(ctx, reporter.activityKey(payload.SessionID))
	if err != nil {
		return err
	}
	defer unlock()
	state, err := reporter.readActivity(payload.SessionID)
	if err != nil {
		return err
	}
	agentEvent := reporter.options.event == herdr.EventAgentStart || reporter.options.event == herdr.EventAgentStop
	// Counts commute by agent id, unlike main-turn reports: do not drop a delayed
	// lifecycle event just because a newer hook acquired the lock first.
	if state.Stored.Seq > reporter.seq && !agentEvent {
		return nil
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
		return reporter.run(ctx, payload)
	}
	// planActivity updates the lifecycle fields, while preserving the pane result
	// separately until all its hold rules have been evaluated.
	action.stored.Agents = state.Stored.Agents
	action.stored.MainStoppedAt = state.Stored.MainStoppedAt
	action.stored.Seq = reporter.seq
	state.Stored = action.stored
	reporter.activity = &state
	reporter.activityAction = action
	err = reporter.run(ctx, payload)
	if reporter.options.socket != "" {
		state.TmuxSocket = reporter.options.socket
		state.TmuxSession = reporter.session
	}
	return errors.Join(err, reporter.finishActivity(payload.SessionID, state, wasWaiting))
}

func (reporter *herdrReporter) removeActivity(session string) error {
	if err := os.Remove(reporter.activityPath(session)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove ended Claude activity: %w", err)
	}
	return nil
}

func (reporter *herdrReporter) finishActivity(session string, state herdrActivity, wasWaiting bool) error {
	if reporter.options.event == herdr.EventEnd {
		return reporter.removeActivity(session)
	}
	if err := reporter.writeActivity(session, state); err != nil {
		return err
	}
	if state.Stored.MainStoppedAt != 0 && len(state.Stored.Agents) > 0 && reporter.deps.spawn != nil &&
		!wasWaiting {
		// The process reads the durable lease, not a timer owned by this short hook.
		if err := reporter.deps.spawn(
			[]string{
				"internal",
				"herdr-state",
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
			return nil, fmt.Errorf("claude agent lifecycle needs hook_event_name %s (skipped)", expected)
		}
		if event == herdr.EventAgentStop && !state.Agents[payload.AgentID] {
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
		if next.State != string(herdr.StateBlocked) {
			next.State = string(herdr.StateWorking)
			next.Message = ""
		}
		action = &herdrAction{stored: next}
	case herdr.EventIdle:
		if state.MainStoppedAt == 0 {
			state.MainStoppedAt = reporter.deps.now().UnixNano()
		}
	case herdr.EventNotification:
		if payload.NotificationType == "idle_prompt" && state.MainStoppedAt == 0 {
			state.MainStoppedAt = reporter.deps.now().UnixNano()
		}
	case herdr.EventWorking:
		// A main-thread tool event also proves an automatic resume. Agent hooks
		// were filtered at entry; stale main hooks are rejected by the sequence.
		if action != nil {
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
	lastError := ""
	for {
		done, err := reporter.expireAgents(args[0], generation)
		if err != nil {
			if err.Error() != lastError {
				reporter.warn("Claude agent watchdog (retrying): %v", err)
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

func (reporter *herdrReporter) expireAgents(session string, generation ...int64) (bool, error) {
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
	var errs []error
	for _, saved := range state.Targets {
		target := herdrTarget{socket: saved.Socket, pane: saved.Pane, terminal: saved.Terminal}
		owner, err := reporter.paneOwner(target)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if owner.Client != saved.Owner {
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
				errs = append(errs, err)
				continue
			}
			target.pane = resolved.Pane
		}
		// The old event sequence is intentional: even an unowned direct pane cannot
		// replace a more recent reporter's state after this watch was scheduled.
		reporter.seq = saved.Seq
		reporter.nativeSeatReport = saved.Native
		errs = append(errs, reporter.send(ctx, target, &herdrAction{stored: state.Stored}, saved.Seq+1))
	}
	errs = append(errs, reporter.writeActivity(session, state))
	return true, errors.Join(errs...)
}
