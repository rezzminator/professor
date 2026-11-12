//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// callDead: from his plain terminal the owner ends the child with `pfm chat end` and calls it: a
// Claude chat that ended with turns in its transcript is dormant, so pfm resumes the call into its own
// session and its transcript grows by that one user record, its footer a block below the text. The
// chat then inspects the ended child at both doors and reopens it (deadChildDoors), ends it, reopens
// and ends it again (reopenedDoors).
func callDead(c *harness.Ctx, s State) State {
	owner := c.Terminal(meshOwner)
	child := s.Mesh.ChildName
	out, errs, code := typedRun(c, owner, "end-child", "pfm chat end "+child)
	if want := "end scheduled " + s.Mesh.Child.Session + "\n"; code != 0 || out != want || errs != "" {
		c.Failf("pfm chat end %s: exit %d, stdout %q, stderr %q; want exit 0, stdout %q", child, code, out, errs, want)
	}
	notLive(c, child)
	before := entries(c, child)
	out, errs, code = typedRun(c, owner, "call-ended", "pfm chat call --allow-unsigned "+child+" ping")
	resumed := regexp.MustCompile(`^called RESUME user turn \S+ \(parent \S+\) into session ` +
		regexp.QuoteMeta(s.Mesh.Child.Session) + ` — answered on next reopen; backup: \S+\n$`)
	// pfm identifies the sender once for the live attempt and the resumed turn it signs, and says once that a
	// plain terminal is no chat.
	if want := unsignedFromOwner; code != 0 || !resumed.MatchString(out) || errs != want {
		c.Failf("pfm chat call %s once it ended: exit %d, stdout %q, stderr %q; want exit 0, stdout matching %s, "+
			"stderr %q", child, code, out, errs, resumed, want)
	}
	if got, want := entries(
		c,
		child,
	), append(
		slices.Clone(before),
		entry{"user", "ping\n\n— " + strings.TrimPrefix(unsignedFooter, "  — ")},
	); !slices.Equal(
		got,
		want,
	) {
		c.Failf("%s's transcript after the resumed call = %+v, want %+v", child, got, want)
	}

	term := c.Terminal(s.Term)
	deadChildDoors(c, term, &s)
	reopenedDoors(c, term, &s)
	return s
}

// deadChat: from his plain terminal the owner starts a Claude chat by name and ends it before its
// first prompt, so its transcript holds its title alone, and calls it: pfm answers it is dead with
// exit 3, names how to revive it, and its transcript stays as it was (escape chat-mesh/call-dead-refused,
// never another transcript matched instead). The chat then plays the dead chat at both doors.
func deadChat(c *harness.Ctx, s *State) {
	owner := c.Terminal(meshOwner)
	owner.WaitFor(prompt)
	dead := c.Unique("dead")
	out, errs, code := typedRun(c, owner, "dead-new", "cd $HOME/work/demo && pfm chat new --engine cc --name "+dead)
	f := strings.Fields(out)
	if code != 0 || errs != "" || len(f) < 3 ||
		out != fmt.Sprintf("cc\t%s\t%s\tnamed\tlisted\nattach: tmux -L %[2]s attach -t %[2]s\n", dead, f[2]) {
		c.Failf("pfm chat new --name %s: exit %d, stdout %q, stderr %q; want exit 0 and its row and attach line",
			dead, code, out, errs)
	}
	c.Cleanup(func() {
		if len(namedChats(c, f[2])) == 0 {
			return
		}
		if r := c.Read("pfm", "chat", "end", dead); r.Code != 0 {
			c.Logf("teardown: pfm chat end %s exited %d: %s%s", dead, r.Code, r.Stderr, r.Stdout)
		}
	})
	var session string
	c.Poll("one live Claude chat named "+dead, stepBound, func() (bool, string) {
		rows := namedChats(c, f[2])
		for id, row := range rows {
			session = id
			return len(rows) == 1 && row == [2]string{"live-claude", dead}, fmt.Sprint(rows)
		}
		return false, "no row"
	})
	out, errs, code = typedRun(c, owner, "dead-end", "pfm chat end "+dead)
	if want := "ended " + session + "\n"; code != 0 || out != want || errs != "" {
		c.Failf("pfm chat end %s: exit %d, stdout %q, stderr %q; want exit 0, stdout %q", dead, code, out, errs, want)
	}
	notLive(c, dead)
	transcript := "~/.claude/projects/*/" + session + ".jsonl"
	title := read(c, "sh", "-c", "cat "+transcript)
	if want := fmt.Sprintf(`{"type":"custom-title","sessionId":%q,"isSidechain":false,"customTitle":%q}`+"\n", session,
		dead); title != want {
		c.Failf("%s's transcript %s before the call = %q, want its title alone: %q", dead, transcript, title, want)
	}
	out, errs, code = typedRun(c, owner, "call-dead", "pfm chat call --allow-unsigned "+dead+" ping")
	refused := fmt.Sprintf(
		"%q is dead (engine cc, session %s); revive it with: pfm chat open %s\n",
		dead,
		session,
		dead,
	)
	if code != 3 || out != "" || !strings.HasSuffix(errs, "\n"+refused) {
		c.Failf("pfm chat call %s: exit %d, stdout %q, stderr %q; want exit 3, no stdout, stderr ending %q",
			dead, code, out, errs, refused)
	}
	if got := read(c, "sh", "-c", "cat "+transcript); got != title {
		c.Failf("%s's transcript %s after the refused call = %q, want it unchanged: %q", dead, transcript, got, title)
	}
	deadChatDoors(c, c.Terminal(s.Term), s, dead, session)
}

// notLive waits until `pfm chat status` answers that name is dead, its exit 3.
func notLive(c *harness.Ctx, name string) {
	c.Poll(name+" dead", 20*time.Second, func() (bool, string) {
		r := c.Read("pfm", "chat", "status", name, "--json")
		var status struct {
			State string `json:"state"`
		}
		return r.Code == 3 && json.Unmarshal([]byte(r.Stdout), &status) == nil && status.State == "dead",
			fmt.Sprintf("exit %d: %s%s", r.Code, r.Stdout, r.Stderr)
	})
}
