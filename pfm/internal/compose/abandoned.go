package compose

import (
	"cmp"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// abandonedLaunch reports whether agent is the launch session its own pane
// already left. Claude's argv keeps the --session-id (or --resume) the pane
// was launched with after a /resume inside it switches threads, so the argv
// id outlives the conversation; the pane's crumb, rewritten by the statusline
// for the thread the pane runs now, is the current identity. The launch id is
// abandoned only when both hold: the same pane's crumb names another thread,
// and the launch thread is empty — never written, or written without a
// prompt. A session with prompts on that pane is a real conversation (a
// headless child, a background job) and keeps its row — unless the agent is
// the pane's own process: one Claude runs one thread, and the crumb names the
// one it runs now, so after a /clear or /resume the launch thread it left is
// no live seat, however many prompts it holds. Kept, it was a second live row
// on the pane under the old title, and the name resolved to the thread nobody
// was writing any more. A native Herdr seat's engine runs beneath the seat's
// host, its own pane process: once the seat names another thread (an in-app
// /resume), the launch thread its argv keeps is no live chat either.
func (current *composer) abandonedLaunch(
	agent gather.Agent,
	transcript store.Transcript,
	indexed bool,
) bool {
	if thread := current.seatHostThread(agent.ParentPID); thread != "" {
		return thread != agent.SessionID
	}
	if agent.Socket == "" {
		return false
	}
	thread, crumbed := current.livePaneThreads[targetKey(agent.Socket, agent.PaneID)]
	if !crumbed || thread == agent.SessionID {
		return false
	}
	if agent.PID != 0 && agent.PID == agent.PanePID {
		return true
	}
	return !indexed || transcript.PromptCount == 0
}

// bootingLaunch reports whether agent is the Claude process of a booting pane, and claims its session
// live. A Claude pfm launched carries its session in argv from birth, so until its statusline first
// renders (held at the folder-trust dialog, say) gather finds it crumbless-live too: the pane's booting
// row is the chat, and an agent row beside it would leave its name matching two live chats.
func (current *composer) bootingLaunch(agent gather.Agent) bool {
	for _, entry := range current.input.Snapshot.CrumblessLive {
		if agent.PID != 0 && entry.PID == agent.PID {
			current.liveTranscripts[agent.SessionID] = struct{}{}
			return true
		}
	}
	return false
}

// seatHostThread is the thread the live native Claude seat hosted by pid carries now (the segment its chat
// continued in, else its session); empty when pid hosts no such seat, its probe failed (a recorded pid
// proves no live host) or the seat has no session yet.
func (current *composer) seatHostThread(pid int) string {
	if pid <= 0 {
		return ""
	}
	for index := range current.input.HerdrSeats {
		native := &current.input.HerdrSeats[index]
		seat := &native.Seat
		if native.Err == nil && seat.Backend == herdrBackend && seat.State == seatLive && seat.SeatHostPID == pid &&
			pfmengine.ID(seat.Engine) == pfmengine.Claude {
			return cmp.Or(native.Continued, seat.SessionID)
		}
	}
	return ""
}
