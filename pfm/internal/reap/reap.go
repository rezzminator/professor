// Package reap classifies and clears the chat socket graveyard.
//
// Closing a terminal tab DETACHES a tmux client but leaves that chat's own tmux
// server — and its ~0.5-1 GB claude process — alive forever; a crashed server
// leaves a 0-RAM socket file behind. Over weeks that is a hundred dead sockets
// and several gigabytes held by chats nobody can see.
//
// Everything here is built around one asymmetry: a socket wrongly KEPT costs
// memory, a socket wrongly KILLED costs a running chat that nobody can get
// back. So the default is a dry run, every unknown fails CLOSED, and a socket
// hosting anything that is not a chat is never reapable at all.
package reap

import (
	"fmt"
	"sort"
	"strings"
	"time"

	pfmengine "hostops/pfm/internal/engine"
)

// State is one socket's verdict, as reported.
type State string

const (
	// StateSelf is the caller's own socket — the one running this command.
	StateSelf State = "self"
	// StateKeep is an attached socket: a live tab is showing it.
	StateKeep State = "keep"
	// StateMate is a cc-new-* detached teammate: headless BY DESIGN, reaped
	// by its parent chat's close choreography, never by a sweep.
	StateMate State = "mate"
	// StateBusy is a session the engine itself reports as working.
	StateBusy State = "busy"
	// StateActive is a session whose transcript was written moments ago — it
	// turned busy after the busy snapshot was taken.
	StateActive State = "active"
	// StateHosts is a socket whose panes host processes that are not chats.
	// A cx socket once carried a project's dev servers; killing its server
	// would have taken them down with it.
	StateHosts State = "hosts"
	// StateFork is an untouched detached /chat:branch seat. It has durable
	// provenance but no transcript yet, so it is explicitly reapable rather
	// than falling into the crumbless busy-unknown guard.
	StateFork State = "fork"
	// StateOrphan is an unattached idle chat — the reapable case.
	StateOrphan State = "orph"
	// StateIdle is an ATTACHED chat whose own last transcript record is older
	// than the horizon and whose client has not moved in the active window
	// either — the gap tmux window_activity and transcript mtime both hid: a
	// chat can sit on the operator's screen for days, statusline redrawing
	// forever, with not one real prompt or response inside that whole span.
	StateIdle State = "IDLE"
	// StateKilled is an orphan whose server this run actually killed.
	StateKilled State = "KILL"
	// StateDead is a socket file with no server behind it, old enough that it
	// cannot be a server still starting up.
	StateDead State = "dead"
	// StateSkip is a socket deliberately left alone, with the reason attached.
	StateSkip State = "SKIP"
	// StateUnknown is a chat whose transcript could not be located, read, or
	// parsed. Absence of a measurement is not a measurement of idleness —
	// StateUnknown is NEVER reapable, whatever its idle time looks like from
	// any other signal.
	StateUnknown State = "UNKN"
)

// Action is what a decision asks the runner to perform.
type Action int

const (
	// ActionNone leaves the socket exactly as it is.
	ActionNone Action = iota
	// ActionKillServer ends a chat's tmux server and clears its handles.
	ActionKillServer
	// ActionRemoveSocketFile removes a socket file with no server behind it.
	ActionRemoveSocketFile
	// ActionKillSession ends one plain-terminal session on the vsct socket.
	ActionKillSession
)

// Socket is one probed chat socket, with everything a verdict needs.
type Socket struct {
	Name string
	// Age is how long ago the socket file was last modified. It is used for
	// ONE thing — telling a corpse apart from a server still starting up — and
	// never as a staleness signal: a chat idle for a week is still a chat.
	Age time.Duration
	// HasServer is true when the probe listed panes. ProbeFailed says the
	// probe could not run at all, which is NOT the same as an empty answer.
	HasServer   bool
	ProbeFailed bool
	ProbeError  string
	Attached    bool
	Label       string
	CWD         string
	RSSKB       int64
	// Foreign names the non-chat processes this socket's panes host.
	Foreign []string
	// CrumbUUIDs are every session id this socket carries, socket crumb AND
	// pane crumbs: a /chat:branch split puts two chats on one socket and the
	// socket crumb is last-writer-wins, so the busy one may not own it.
	CrumbUUIDs []string
	HasCrumb   bool
	// DetachedFork is backed by the shared branch-seat marker. It identifies
	// provenance only; busy, recent, attached and hosted-work guards still win.
	DetachedFork bool
	// transcripts holds each CrumbUUIDs entry's file, appended in lockstep
	// with it, so the recency check has a path to stat without searching for
	// one. It stays unexported because it is probe plumbing, not a verdict
	// input: Plan never reads it.
	transcripts []string
	// ActivityPaths is every transcript or rollout this socket's chat(s)
	// write — Claude crumb paths and Codex rollout paths alike. It feeds
	// ONLY the idle-horizon check below; unlike transcripts it carries no
	// lockstep index into CrumbUUIDs; nothing else reads it.
	ActivityPaths []string
	// IdleFor is how long ago the newest record across ActivityPaths was
	// written — never window_activity, never a raw file mtime, both proven
	// to lie at this timescale on the live fleet. Meaningful only when
	// ActivityOK.
	IdleFor time.Duration
	// ActivityOK is false when no path in ActivityPaths could be resolved,
	// read, or parsed. False means UNKNOWN, and UNKNOWN is never reaped.
	ActivityOK bool
	// ClientIdle is how long ago #{client_activity} last moved for the most
	// recently active client attached to this socket. Meaningful ONLY when
	// ClientIdleOK — a zero-value ClientIdle on an unanswered probe must
	// never be read as "just active".
	ClientIdle time.Duration
	// ClientIdleOK is true only when the client-activity probe actually
	// answered: found && err == nil. False covers two different failures —
	// the probe could not run at all (ClientProbeError set), or it ran and
	// found NO client though the socket reads as Attached, which is the
	// 17-day zombie-client shape this feature exists to catch (a foreground
	// `tmux new-session` client whose terminal died, still counted
	// attached). Neither may render as "active"; planAttached sends both to
	// StateUnknown.
	ClientIdleOK bool
	// ClientProbeError is set only when the probe itself errored (timeout,
	// permission, socket gone) — distinct from a clean answer of zero
	// clients, which leaves this empty.
	ClientProbeError string
}

// VSCTSession is one plain-terminal bunker session on the shared vsct socket.
type VSCTSession struct {
	Name     string
	Attached bool
	Idle     time.Duration
}

// Input is one sweep's complete, already-probed world.
type Input struct {
	Self    string
	Apply   bool
	Sockets []Socket
	VSCT    []VSCTSession
	// AgentsOK is false when the busy query failed. With the busy set
	// unknown, every socket carrying a chat is skipped rather than guessed at.
	AgentsOK    bool
	BusyIDs     map[string]struct{}
	RecentIDs   map[string]struct{}
	BusyRecent  time.Duration
	DeadAfter   time.Duration
	VSCTMaxIdle time.Duration
	// Horizon is how long an ATTACHED chat may sit with no last-transcript-
	// record activity before it is reapable. Default 48h — the order was
	// "idle means no activity, 0, unless it's open for me".
	Horizon time.Duration
	// ClientActive is exemption 1's window: a socket whose most recently
	// active attached client moved inside this span is NEVER reaped,
	// whatever its transcript idle time. Default one hour.
	ClientActive time.Duration
}

// Decision is one socket's verdict plus the action it earns.
type Decision struct {
	Socket string
	State  State
	Reason string
	RSSKB  int64
	Label  string
	CWD    string
	Action Action
	// Failed is set by apply — never by Plan — when an action was actually
	// attempted and the attempt itself errored (a kill-server that failed, a
	// socket file remove that failed). It is NOT set when apply correctly
	// declined a stale plan (the chat re-attached, the socket answered again
	// before removal): those are the safety exemptions working, not a
	// failure. A sweep with any Failed decision must exit non-zero — a
	// report of success that quietly failed to kill is the exact failure
	// mode this command exists to prevent.
	Failed bool
}

// Reapable reports whether a decision asks for a destructive action.
func (decision Decision) Reapable() bool {
	return decision.Action != ActionNone
}

// Plan is the whole classification, and the only place a socket's fate is
// decided. It performs nothing: the runner executes what it returns, so every
// rule here is table-testable without a tmux server in sight.
//
// The dry run and the apply run classify IDENTICALLY. The shell original
// reported a fail-closed skip as a plain orphan until you actually ran the
// reap, so its preview promised kills that never happened; a preview that does
// not match the run it previews is not a preview.
func Plan(input Input) []Decision {
	decisions := make([]Decision, 0, len(input.Sockets)+len(input.VSCT))
	for index := range input.Sockets {
		socket := input.Sockets[index]
		decisions = append(decisions, planSocket(input, socket))
	}
	for _, session := range input.VSCT {
		decisions = append(decisions, planVSCT(input, session))
	}
	sort.SliceStable(decisions, func(left, right int) bool {
		return decisions[left].Socket < decisions[right].Socket
	})
	return decisions
}

func planSocket(input Input, socket Socket) Decision {
	decision := Decision{
		Socket: socket.Name,
		RSSKB:  socket.RSSKB,
		Label:  socket.Label,
		CWD:    socket.CWD,
	}

	// A probe that could not RUN never reports "nothing found": a tmux call
	// that failed for any reason other than "no server there" says nothing
	// about the socket, and silence would read as a corpse.
	if socket.ProbeFailed {
		decision.State = StateSkip
		decision.Reason = "probe failed — " + socket.ProbeError
		return decision
	}

	if !socket.HasServer {
		// A forking tmux binds its socket BEFORE its session exists, and that
		// reads identically to dead. Only a socket file old enough to rule
		// that out is a corpse.
		if socket.Age < input.DeadAfter {
			decision.State = StateSkip
			decision.Reason = "empty socket younger than " +
				durationWords(input.DeadAfter) + " — a server may still be starting"
			return decision
		}
		decision.State = StateDead
		decision.Reason = "stale socket file"
		if input.Apply {
			decision.Action = ActionRemoveSocketFile
		}
		return decision
	}

	if strings.HasPrefix(socket.Name, "cc-new-") {
		decision.State = StateMate
		decision.Reason = "detached teammate — its parent chat's close choreography reaps it"
		return decision
	}

	for _, uuid := range socket.CrumbUUIDs {
		if _, busy := input.BusyIDs[uuid]; busy {
			decision.State = StateBusy
			decision.Reason = "engine reports this session busy"
			return decision
		}
		if _, recent := input.RecentIDs[uuid]; recent {
			decision.State = StateActive
			decision.Reason = "transcript written within " +
				durationWords(input.BusyRecent)
			return decision
		}
	}

	if socket.Name == input.Self {
		decision.State = StateSelf
		decision.Reason = "this command's own chat"
		return decision
	}

	// The socket is idle by every chat signal so far — but a chat's panes are
	// a SHELL, and a shell hosts whatever was typed into it. Killing the
	// server kills all of it, so anything that is not a chat makes the
	// socket load-bearing and unreapable however long it has been quiet,
	// attached or not — checked here so it guards BOTH branches below.
	if len(socket.Foreign) > 0 {
		decision.State = StateHosts
		decision.Reason = "hosts non-chat processes: " +
			strings.Join(socket.Foreign, ", ")
		return decision
	}

	if socket.Attached {
		return planAttached(input, socket, decision)
	}

	if socket.DetachedFork && !socket.HasCrumb {
		decision.State = StateFork
		decision.Reason = "untouched detached fork"
		if input.Apply {
			decision.Action = ActionKillServer
		}
		return decision
	}

	// Fail closed twice over. With the busy set unknown, a chat here could be
	// grinding a task; with no breadcrumb, its busy state was never readable
	// in the first place. Codex writes no breadcrumb BY DESIGN and explicitly
	// accepts the tab-or-die tradeoff. Every other known engine fails closed.
	if !input.AgentsOK && socket.HasCrumb {
		decision.State = StateSkip
		decision.Reason = "busy-unknown (busy query failed)"
		return decision
	}
	id, known := pfmengine.FromSocket(socket.Name)
	if !socket.HasCrumb && known && id != pfmengine.Codex {
		decision.State = StateSkip
		decision.Reason = "busy-unknown (no breadcrumb)"
		return decision
	}

	decision.State = StateOrphan
	decision.Reason = "unattached, idle"
	if input.Apply {
		decision.Action = ActionKillServer
	}
	return decision
}

// planAttached judges a socket a live client is showing right now — the gap
// the two proven-wrong signals left wide open. tmux's own #{window_activity}
// answers "active" whether the chat is busy or abandoned, because the TUI
// redraws its statusline forever; the transcript file's mtime gets touched
// by bookkeeping records every 30-60 minutes with no conversational activity
// behind it. Neither can tell a chat that has been open on the operator's
// screen for weeks from one mid-turn right now. The only signal that held up
// is the timestamp inside the transcript's own last parseable record.
func planAttached(input Input, socket Socket, decision Decision) Decision {
	// A probe that could not run, or one that ran and found NO client
	// though the socket reads as Attached, is UNKNOWN — never "active".
	// Reading socket.ClientIdle's zero value as freshness here is exactly
	// the coincidence detector this whole feature exists to stop: it is the
	// 17-day zombie-client shape (a foreground tmux client whose terminal
	// died, still counted attached) rendering as the most reassuring
	// sentence the command can print.
	if !socket.ClientIdleOK {
		decision.State = StateUnknown
		if socket.ClientProbeError != "" {
			decision.Reason = "attached, but the client-activity probe could not run — " +
				socket.ClientProbeError +
				"; absence of a measurement is not a measurement of idleness"
		} else {
			decision.Reason = "attached, but no client answered — the socket reads as " +
				"attached with nobody there; absence of a measurement is not a measurement of idleness"
		}
		return decision
	}
	if socket.ClientIdle < input.ClientActive {
		decision.State = StateKeep
		decision.Reason = "attached, active within " + durationWords(input.ClientActive)
		return decision
	}
	if !socket.ActivityOK {
		decision.State = StateUnknown
		decision.Reason = "attached, client idle past " + durationWords(input.ClientActive) +
			" — transcript could not be read; absence of a measurement is not a measurement of idleness"
		return decision
	}
	if socket.IdleFor >= input.Horizon {
		decision.State = StateIdle
		decision.Reason = "idle " + durationWords(socket.IdleFor) +
			" — past the " + durationWords(input.Horizon) + " horizon"
		if input.Apply {
			decision.Action = ActionKillServer
		}
		return decision
	}
	decision.State = StateKeep
	decision.Reason = "attached, idle " + durationWords(socket.IdleFor) +
		" (under the " + durationWords(input.Horizon) + " horizon)"
	return decision
}

func planVSCT(input Input, session VSCTSession) Decision {
	decision := Decision{
		Socket: "vsct:" + session.Name,
		Label:  "plain terminal",
	}
	if session.Attached || session.Idle < input.VSCTMaxIdle {
		decision.State = StateKeep
		decision.Reason = "attached or recently active"
		return decision
	}
	decision.State = StateOrphan
	decision.Reason = "idle " + durationWords(session.Idle)
	if input.Apply {
		decision.Action = ActionKillSession
	}
	return decision
}

func durationWords(value time.Duration) string {
	switch {
	case value >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(value.Hours())/24)
	case value >= time.Hour:
		return value.Round(time.Hour).String()
	case value >= time.Minute:
		return value.Round(time.Minute).String()
	default:
		return value.Round(time.Second).String()
	}
}
