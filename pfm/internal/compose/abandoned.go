package compose

import (
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
// headless child, a background job) and keeps its row.
func (current *composer) abandonedLaunch(
	agent gather.Agent,
	transcript store.Transcript,
	indexed bool,
) bool {
	if agent.Socket == "" {
		return false
	}
	thread, crumbed := current.livePaneThreads[targetKey(agent.Socket, agent.PaneID)]
	if !crumbed || thread == agent.SessionID {
		return false
	}
	return !indexed || transcript.PromptCount == 0
}
