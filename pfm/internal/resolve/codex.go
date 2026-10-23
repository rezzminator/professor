package resolve

import (
	"fmt"
	"path/filepath"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

const (
	// CodexThreadEnv is the variable a Codex session exports into the shells
	// it spawns. It names the conversation outright, which nothing else about
	// a live process does: one codex app-server process holds every thread
	// writer lock, so pid ancestry cannot tell two Codex sessions apart.
	CodexThreadEnv = "CODEX_THREAD_ID"

	// CodexBirthWindowSeconds bounds the fallback that pairs a pane with the
	// thread created at about the same moment in the same directory.
	CodexBirthWindowSeconds = 120
)

// CodexThread is one candidate conversation offered by the Codex state store.
type CodexThread struct {
	ID          string
	CWD         string
	CreatedAt   int64
	RolloutPath string
}

// CodexThreadID names the Codex conversation behind a pane. bound — the
// pane's own kill.Manager binding, when the caller has one — outranks
// everything else: the pane's TUI process is never restarted by /clear, so
// matching it by birth time or an exported CODEX_THREAD_ID keeps returning
// the PRE-clear thread forever, while the binding is what a pane-status-line
// reconcile pass (fleet.ReconcileCodexPanes) actively advances across a
// clear. Without a binding, an exported CODEX_THREAD_ID is authoritative and
// is honored even when the state store has not caught up with it. Without
// either, the pane is matched to the thread created nearest its own start
// within CodexBirthWindowSeconds in the same directory; a tie prefers the
// newer thread. A pane that matches nothing returns an error rather than a
// guess, because a wrong identity would attach, rename, or kill the wrong
// chat.
func CodexThreadID(
	exported string,
	bound string,
	cwd string,
	birth int64,
	candidates []CodexThread,
) (CodexThread, error) {
	if bound != "" {
		return matchCodexThreadByID(bound, candidates), nil
	}
	if exported != "" {
		return matchCodexThreadByID(exported, candidates), nil
	}
	if cwd == "" || birth <= 0 {
		return CodexThread{}, fmt.Errorf(
			"no %s is exported and the pane has no directory and start time to match a Codex thread",
			CodexThreadEnv,
		)
	}

	wanted := filepath.Clean(cwd)
	best := CodexThread{}
	bestDistance := int64(-1)
	for _, candidate := range candidates {
		if candidate.ID == "" ||
			candidate.CWD == "" ||
			filepath.Clean(candidate.CWD) != wanted {
			continue
		}
		distance := birth - candidate.CreatedAt
		if distance < 0 {
			distance = -distance
		}
		if distance > CodexBirthWindowSeconds {
			continue
		}
		if bestDistance < 0 ||
			distance < bestDistance ||
			(distance == bestDistance && newerCodexThread(candidate, best)) {
			best = candidate
			bestDistance = distance
		}
	}
	if bestDistance < 0 {
		return CodexThread{}, fmt.Errorf(
			"no Codex thread in %q was created within %ds of the pane",
			wanted,
			CodexBirthWindowSeconds,
		)
	}
	return best, nil
}

// matchCodexThreadByID looks a known thread id up among candidates for its
// rollout path; an id the state store has not caught up with yet still
// resolves, bare, rather than being refused.
func matchCodexThreadByID(id string, candidates []CodexThread) CodexThread {
	for _, candidate := range candidates {
		if candidate.ID == id {
			return candidate
		}
	}
	return CodexThread{ID: id}
}

func newerCodexThread(challenger, incumbent CodexThread) bool {
	if challenger.CreatedAt != incumbent.CreatedAt {
		return challenger.CreatedAt > incumbent.CreatedAt
	}
	return challenger.ID < incumbent.ID
}

// CodexSeat is the live-seat projection shared by CLI and MCP identity.
type CodexSeat struct {
	ThreadID, Backend, SeatID, SessionName, SocketPath, Pane, HerdrWorkspace, BackendError string
}

// CodexThreadSeat resolves exactly one live seat holding thread.
func CodexThreadSeat(thread string, seats []CodexSeat) (Identity, error) {
	var match CodexSeat
	count := 0
	for i := range seats {
		seat := &seats[i]
		if seat.ThreadID == thread && thread != "" {
			match = *seat
			count++
		}
	}
	if count == 0 {
		return Identity{}, fmt.Errorf("%w: no live Codex seat holds thread %s", ErrNotInChat, thread)
	}
	if count != 1 {
		return Identity{}, fmt.Errorf("%w: multiple live Codex seats hold thread %s", ErrAmbiguous, thread)
	}
	if match.BackendError != "" {
		return Identity{}, fmt.Errorf("look up the caller: Codex seat %s: %s", match.SeatID, match.BackendError)
	}
	session := match.SessionName
	if session == "" {
		if match.Backend == nativeHerdrBackend {
			session = match.SeatID
		} else {
			session = filepath.Base(match.SocketPath)
		}
	}
	if match.SocketPath == "" || session == "" ||
		match.Backend == nativeHerdrBackend && (match.Pane == "" || match.HerdrWorkspace == "") {
		return Identity{}, fmt.Errorf("look up the caller: Codex seat %s has malformed identity", match.SeatID)
	}
	return Identity{
		Backend:        match.Backend,
		SeatID:         match.SeatID,
		Session:        session,
		SocketPath:     match.SocketPath,
		SocketName:     filepath.Base(match.SocketPath),
		Pane:           match.Pane,
		HerdrWorkspace: match.HerdrWorkspace,
		Engine:         string(pfmengine.Codex),
		ID:             thread,
		Source:         "codex-thread",
		Recovered:      true,
	}, nil
}
