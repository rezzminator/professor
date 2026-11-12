//go:build journey

package journey

import (
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// callAfterRestore: from a second terminal, outside every chat, the owner calls the first resumed chat
// by its name (`pfm chat call --allow-unsigned`, as pfm asks of a caller with no identity); the call
// lands live in the chat's pane, the prompt reaches its transcript once, and the chat answers.
func callAfterRestore(c *harness.Ctx, s State) State {
	cli := c.Terminal(s.Term + "-cli")
	reply := c.Unique("pong")
	command := "clear && pfm chat call --allow-unsigned " + s.Name + ` 'ping mock-engine: {"type":"turn","reply":"` + reply + `"}'`
	if code := shellRun(cli, "call-"+reply, command, 30*time.Second); code != 0 {
		c.Failf("pfm chat call %s exited %d:\n%s", s.Name, code, cli.Screen())
	}
	if want := `called LIVE into "` + s.Pane + `"`; !strings.Contains(unwrapped(cli.Screen()), want) {
		c.Failf("pfm chat call printed no %q:\n%s", want, cli.Screen())
	}
	c.Poll(s.Name+" answered "+reply, 20*time.Second, func() (bool, string) {
		r := c.Read("pfm", "chat", "last", s.Name)
		return r.Code == 0 && r.Stdout == reply+"\n", r.Stdout + r.Stderr
	})
	transcript := read(c, "sh", "-c", "cat "+s.Home+"/.cc/1/projects/*/"+s.Chat.Session+".jsonl")
	if got := strings.Count(transcript, "ping mock-engine: "); got != 1 {
		c.Failf("the call's prompt is in %s's transcript %d times, want 1", s.Chat.Session, got)
	}
	return s
}
