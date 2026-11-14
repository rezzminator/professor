//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// herdrReturns: the owner starts the restart chain's Herdr session again where it ran. Herdr restores
// both tabs as it saved them and, its own agent resume on (resume_agents_on_restore, its default),
// resumes each chat itself: it types the chat's resume into the restored pane's shell as the shell
// starts, and pfm's auto-open picker yields to it (the shim asks `pfm internal herdr-shell
// resume-pending`). No picker opens on either tab and none reads Herdr's keys as its find text; each
// chat is back live by itself on its own pane under its old session and account, with a live seat and
// one `pfm ls` row, its tab still under its name and the idle mark. The moment the first chat reads live
// the owner reloads it inside Herdr's startup grace. The picker's ↻-row resume of a chat whose engine
// died is resumeFromPicker's (herdr-native).
func herdrReturns(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	term.Type(strings.Join(herdrCLI(s), " ") + "\r")
	want := map[string]string{}
	for _, chat := range s.Chats {
		want[chat.Pane] = markedLabel("idle", chat.Name)
	}
	sock := herdrSocket(s)
	live := func(chat Chat) (bool, string) {
		rows, _ := liveChats(c, sock)
		seats := liveSeats(c, sock)
		seat, ok := seats[chat.Session]
		listed := listRows(c, chat.Session)
		return ok && seat[1] == chat.Pane && rows[chat.Session][0] == "live-claude" && len(listed) == 1,
			fmt.Sprintf("chats %v, seats %v, ls rows of %s %q", rows, seats, chat.Session, listed)
	}
	c.Poll(s.Chats[0].Name+" live again by itself under "+s.Chats[0].Session, 30*time.Second, func() (bool, string) {
		return live(s.Chats[0])
	})
	s.Chats[0].Seat = liveSeats(c, sock)[s.Chats[0].Session][0]
	reloadInGrace(c, s, s.Chats[0])
	c.Poll("every chat live again by itself on its own pane", 30*time.Second, func() (bool, string) {
		for _, chat := range s.Chats {
			if ok, seen := live(chat); !ok {
				return false, chat.Name + " not yet: " + seen
			}
		}
		if seats := liveSeats(c, sock); len(seats) != len(s.Chats) {
			return false, fmt.Sprintf("seats %v, want %d", seats, len(s.Chats))
		}
		return true, ""
	})
	for i, chat := range s.Chats {
		s.Chats[i].Seat = liveSeats(c, sock)[chat.Session][0]
		s.Chat = s.Chats[i]
		s.Chats[i].Engine = engineOf(c, s)
		env, err := procTable(c).Environ(s.Chats[i].Engine)
		if err != nil || env["CLAUDE_CONFIG_DIR"] != chat.Account {
			c.Failf("resumed engine %d of %s runs on CLAUDE_CONFIG_DIR %q (%v), want %q as before the stop",
				s.Chats[i].Engine, chat.Name, env["CLAUDE_CONFIG_DIR"], err, chat.Account)
		}
		term.Press(harness.Ctrl('b'), harness.Key(fmt.Sprint(i+1)))
		c.Poll("tab "+chat.Name+" focused", stepBound, func() (bool, string) {
			got := focusedPane(c, s)
			return got == chat.Pane, "focused pane " + got
		})
		// Herdr's server focuses the tab before its client draws it: the tab is drawn once the screen
		// shows this chat's own status line under its composer.
		term.WaitScreen("tab "+chat.Name+" drawn with its chat", stepBound, func(screen string) bool {
			return strings.Contains(screen, "🔖 "+chat.Name+" ") && strings.Contains(screen, "❯")
		})
		if screen := term.Screen(); strings.Contains(screen, "find ›") || strings.Contains(screen, "↻ ") {
			c.Failf("tab %s shows pfm's picker after Herdr resumed its chat:\n%s", chat.Name, screen)
		}
	}
	c.Poll("each restored tab under its chat's name", stepBound, func() (bool, string) {
		got := paneTabs(c, s)
		return fmt.Sprint(got) == fmt.Sprint(want), fmt.Sprintf("tabs %v, want %v", got, want)
	})
	s.Chat, s.Pane, s.Name = s.Chats[0], s.Chats[0].Pane, s.Chats[0].Name
	fleetConsistent(c, s)
	return s
}

// focusedPane is the pane Herdr reports focused in the branch's session, "" when none is.
func focusedPane(c *harness.Ctx, s State) string {
	var list struct {
		Result struct {
			Panes []struct {
				PaneID  string `json:"pane_id"`
				Focused bool   `json:"focused"`
			} `json:"panes"`
		} `json:"result"`
	}
	out := read(c, herdrCLI(s, "pane", "list")...)
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		c.Failf("herdr pane list: %v:\n%s", err, out)
	}
	for _, pane := range list.Result.Panes {
		if pane.Focused {
			return pane.PaneID
		}
	}
	return ""
}

// reloadInGrace: the moment the first resumed chat reads live, while Herdr may still read it unknown in its
// startup grace, the owner aims `pfm chat reload` at its pane from the second terminal; the reload waits the
// grace out instead of refusing, and the chat is back on its seat and session under a new engine.
func reloadInGrace(c *harness.Ctx, s State, chat Chat) {
	before := 0
	for pid, session := range engines(processes(c)) {
		if session == chat.Session {
			before = pid
		}
	}
	command := "pfm chat reload --sock " + herdrSocket(s) + " --pane " + chat.Pane
	if code := shellRun(c.Terminal(s.Term+"-cli"), "grace-reload", command, 30*time.Second); code != 0 {
		c.Failf("%s right after the resume exited %d, want 0", command, code)
	}
	c.Poll("a new engine for "+chat.Session, 20*time.Second, func() (bool, string) {
		for pid, session := range engines(processes(c)) {
			if session == chat.Session && pid != before {
				return true, ""
			}
		}
		return false, fmt.Sprintf("engines %v (before %d)", engines(processes(c)), before)
	})
	s.Chat = chat
	reloadRunDone(c, s)
	if seat := liveSeats(c, herdrSocket(s))[chat.Session]; seat != [2]string{chat.Seat, chat.Pane} {
		c.Failf("the seat of %s after the reload = %v, want %v", chat.Session, seat, [2]string{chat.Seat, chat.Pane})
	}
}
