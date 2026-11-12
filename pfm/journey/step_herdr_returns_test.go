//go:build journey

package journey

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// herdrActiveTab opens the label of the tab Herdr's client draws active: its theme's accent background,
// then the label padded by two spaces.
const herdrActiveTab = "48;2;137;180;250m  "

// herdrReturns: the owner starts the restart chain's Herdr session again where it ran. Herdr restores
// both tabs under their chats' names, each pane on pfm's picker and no engine started: with Herdr's
// own agent resume on (resume_agents_on_restore, its default) nothing of a pfm chat comes back by
// itself. The owner resumes each chat the way the picker offers it, in its own tab: prefix and the tab's
// number, its name typed into the picker's find, Enter: a chat whose engine died keeps its resume
// recipe, so no wizard asks again. Each chat is back live on its own pane under its old session, its
// tab still under its name.
func herdrReturns(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	term.Type(strings.Join(herdrCLI(s), " ") + "\r")
	term.WaitFor("↻ "+s.Chats[len(s.Chats)-1].Name, 20*time.Second)
	want := map[string]string{}
	for _, chat := range s.Chats {
		want[chat.Pane] = chat.Name
	}
	if got := paneTabs(c, s); fmt.Sprint(got) != fmt.Sprint(want) {
		c.Failf("the tabs Herdr restored = %v, want %v", got, want)
	}
	for pid, session := range engines(processes(c)) {
		for _, chat := range s.Chats {
			if session == chat.Session {
				c.Failf("engine %d of %s started on Herdr's restore, before the owner resumed it", pid, chat.Name)
			}
		}
	}
	sock := herdrSocket(s)
	for i, chat := range s.Chats {
		term.Press(harness.Ctrl('b'), harness.Key(fmt.Sprint(i+1)))
		c.Poll("tab "+chat.Name+" focused", stepBound, func() (bool, string) {
			got := focusedPane(c, s)
			return got == chat.Pane, "focused pane " + got
		})
		// Herdr's server focuses the tab before its client draws it: keys typed in between reach the
		// tab the client still shows. The client has drawn the switch once its last active tab label is
		// this tab's.
		c.Poll("the client drew tab "+chat.Name+" active", stepBound, func() (bool, string) {
			raw := term.Raw()
			at := bytes.LastIndex(raw, []byte(herdrActiveTab+chat.Name+"  "))
			for _, other := range s.Chats {
				if other.Name != chat.Name && bytes.LastIndex(raw, []byte(herdrActiveTab+other.Name+"  ")) > at {
					return false, "the client still draws tab " + other.Name + " active"
				}
			}
			return at >= 0, "the client drew no tab " + chat.Name + " active"
		})
		term.WaitScreen("the picker of tab "+chat.Name, stepBound, func(screen string) bool {
			return strings.Contains(screen, "↻ "+chat.Name) && strings.Contains(screen, "find › type to find") &&
				!strings.Contains(screen, "⟳ refreshing")
		})
		term.Type(chat.Name)
		term.WaitScreen("the picker narrowed to "+chat.Name, stepBound, func(screen string) bool {
			return strings.Contains(screen, "find › "+chat.Name)
		})
		if !strings.HasPrefix(selectedRow(term.Screen()), "↻ "+chat.Name) {
			term.Press(harness.Down)
		}
		term.WaitScreen("the chat "+chat.Name+" selected", stepBound, func(screen string) bool {
			return strings.HasPrefix(selectedRow(screen), "↻ "+chat.Name)
		})
		term.Press(harness.Enter)
		term.WaitFor("❯", 20*time.Second)
		c.Poll(chat.Name+" live again under "+chat.Session+" on "+chat.Pane, 20*time.Second, func() (bool, string) {
			rows, _ := liveChats(c, sock)
			seats := liveSeats(c, sock)
			seat, ok := seats[chat.Session]
			if !ok || seat[1] != chat.Pane || rows[chat.Session][0] != "live-claude" || len(seats) != i+1 {
				return false, fmt.Sprintf("chats %v, seats %v", rows, seats)
			}
			s.Chats[i].Seat = seat[0]
			return true, ""
		})
	}
	c.Poll("each restored tab under its chat's name", stepBound, func() (bool, string) {
		got := paneTabs(c, s)
		return fmt.Sprint(got) == fmt.Sprint(want), fmt.Sprintf("tabs %v, want %v", got, want)
	})
	for i := range s.Chats {
		s.Chat = s.Chats[i]
		s.Chats[i].Engine = engineOf(c, s)
	}
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
