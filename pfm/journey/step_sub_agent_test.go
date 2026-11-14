//go:build journey

package journey

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/journey/harness"
)

// subAgent: the owner starts two background agents and the main turn ends; Herdr and pfm keep the
// chat working while they run, and pfm lists no row for either. A question opens; one agent ends
// under it, and the chat stays blocked. Answered, that agent's result arrives and the chat is working
// again for the other; when the last one ends its result turn works, then the chat idles, still the
// only chat on the branch's socket.
func subAgent(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	first, sibling, reply := c.Unique("agent"), c.Unique("agent"), c.Unique("reply")
	firstGate, siblingGate := agentGate(c, first), agentGate(c, sibling)
	agentStep := func(name, gate string) string {
		return `{"type":"background_agent","name":"` + name + `","until_gone":"` + gate + `","busy_ms":4000}`
	}
	term.Type(`two agents mock-engine: [` + agentStep(first, firstGate) + "," + agentStep(sibling, siblingGate) +
		`,{"type":"turn","reply":"` + reply + `"}]` + "\r")
	term.WaitFor("⏺ "+reply, 20*time.Second)
	term.WaitFor("✻ Waiting for 2 background agents to finish")
	awaitAttention(c, s, "working", stepBound)
	holdAttention(c, s, "working", 3)
	onlyChat(c, s, first, sibling)

	ask(c, s)
	endAgent(c, siblingGate)
	term.WaitGone("● "+sibling+"  running", stepBound)
	holdAttention(c, s, "blocked", 3)
	term.Press(harness.Enter)
	term.WaitFor(`⏺ Agent "`+sibling+`" finished`, 20*time.Second)
	term.WaitFor("✻ Waiting for 1 background agent to finish")
	awaitAttention(c, s, "working", stepBound)

	endAgent(c, firstGate)
	awaitAttention(c, s, "working", 4*time.Second)
	term.WaitFor(`⏺ Agent "`+first+`" finished`, 20*time.Second)
	term.WaitGone("background agent", stepBound)
	awaitAttention(c, s, "idle", stepBound)
	onlyChat(c, s, first, sibling)
	fleetConsistent(c, s)
	return s
}

// onlyChat requires the branch's chat to be the only live chat on its socket and no row of pfm's list
// to name one of its agents.
func onlyChat(c *harness.Ctx, s State, agents ...string) {
	rows, listed := liveChats(c, herdrSocket(s))
	if _, ok := rows[s.Chat.Session]; !ok || len(listed) != 1 {
		c.Failf("live chats on %s = %v, want only %s", herdrSocket(s), listed, s.Chat.Session)
	}
	var named []string
	for _, line := range strings.Split(read(c, "pfm", "ls", "--tsv"), "\n") {
		for _, agent := range agents {
			// a whole name: agent-x is not inside another branch's agent-x-2
			if regexp.MustCompile(`(^|[^a-z0-9-])` + regexp.QuoteMeta(agent) + `($|[^a-z0-9-])`).MatchString(line) {
				named = append(named, line)
			}
		}
	}
	if len(named) != 0 {
		c.Failf("pfm lists a row for a background agent:\n%s", strings.Join(named, "\n"))
	}
}

// agentGate is the file a fake background agent named name runs until; it is removed when the
// step's subtree ends, whatever became of the agent.
func agentGate(c *harness.Ctx, name string) string {
	dir, err := paths.TempDir("journey")
	if err != nil {
		c.Failf("scratch directory for the gate of agent %s: %v", name, err)
	}
	gate := filepath.Join(dir, name)
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		c.Failf("create the gate of agent %s: %v", name, err)
	}
	c.Cleanup(func() {
		if err := os.Remove(gate); err != nil && !errors.Is(err, fs.ErrNotExist) {
			c.Logf("remove agent gate %s: %v", gate, err)
		}
	})
	return gate
}

// endAgent ends the fake background agent waiting on gate.
func endAgent(c *harness.Ctx, gate string) {
	if err := os.Remove(gate); err != nil {
		c.Failf("end the agent of %s: %v", gate, err)
	}
	c.Logf("%s", fmt.Sprintf("agent gate %s removed", gate))
}
