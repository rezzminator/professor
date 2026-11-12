//go:build journey

package journey

import (
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// reloadAgain: with a background agent running behind the idle box, which Herdr reads as working, the
// owner types /reload a second time; the reload is not refused, and the conversation /clear started
// comes back under one new engine resumed on it (reloadTyped's checks), its status line drawn.
func reloadAgain(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	agent, reply := c.Unique("agent"), c.Unique("reply")
	gate := agentGate(c, agent)
	term.Type(c.Unique("agents") + ` mock-engine: [{"type":"background_agent","name":"` + agent +
		`","until_gone":"` + gate + `"},{"type":"turn","reply":"` + reply + `"}]` + "\r")
	term.WaitFor("⏺ "+reply, 20*time.Second)
	term.WaitFor("✻ Waiting for 1 background agent to finish")
	awaitAttention(c, s, "working", stepBound)
	s = reloadTyped(c, s)
	requireStatusRow(c, s)
	return s
}
