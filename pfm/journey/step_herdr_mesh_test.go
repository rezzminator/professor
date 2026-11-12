//go:build journey

package journey

import (
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// meshOwner is herdr-mesh's plain terminal, where the owner runs pfm's commands beside his Herdr session.
const meshOwner = "mesh-owner"

// Mesh is what herdr-mesh's steps hand on beside the branch's own chat: the chat it spawned through
// pfm's MCP, and the tmux-hosted chat it calls across multiplexers.
type Mesh struct {
	Child     Chat   // the chat child-new spawned
	ChildName string // its pfm name
	ChildPane string // its Herdr pane
	Peer      Chat   // the tmux-hosted chat call-across started
	PeerName  string // its pfm name
	PeerSock  string // its tmux socket
}

// herdrMesh: in a plain terminal the owner turns pfm's chat MCP server on (`pfm mcp chat enable`, off
// by default), so the chats he starts next can reach each other; pfm's list of servers then reads it
// on from his config. Then he starts a Herdr session of the branch's own. When the branch ends the
// server is turned off again, so no later step meets a config the day changed.
func herdrMesh(c *harness.Ctx, s State) State {
	owner := c.Terminal(meshOwner)
	owner.WaitFor(prompt)
	if code := shellRun(owner, "mcp-enable", "pfm mcp chat enable", stepBound); code != 0 {
		c.Failf("pfm mcp chat enable exited %d:\n%s", code, owner.Screen())
	}
	c.Cleanup(func() {
		if r := c.Read("pfm", "mcp", "chat", "disable"); r.Code != 0 {
			c.Logf("teardown: pfm mcp chat disable exited %d: %s%s", r.Code, r.Stderr, r.Stdout)
		}
	})
	if got, want := strings.TrimSpace(read(c, "pfm", "mcp", "ls")), "chat\ttrue\tfile"; got != want {
		c.Failf("pfm mcp ls after the enable = %q, want %q", got, want)
	}
	return herdrSession(c, s)
}
