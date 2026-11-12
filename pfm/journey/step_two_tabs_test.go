//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// twoTabs: in the restart chain's own Herdr session the owner starts a Claude chat from the first
// pane's picker, opens a second tab with Herdr's new-tab key (prefix c, its name left at Herdr's
// default) and starts a second Claude chat from its picker, then names each chat from a second
// terminal; pfm lists both live on that session's socket, each on its own pane, its tab under its name.
func twoTabs(c *harness.Ctx, s State) State {
	s = claudeFromPicker(c, s)
	first := s.Chat
	first.Pane = s.Pane
	term := c.Terminal(s.Term)
	term.Press(harness.Ctrl('b'), harness.Key("c"))
	term.WaitFor("new tab")
	term.Press(harness.Enter)
	term.WaitFor("New chat", 20*time.Second)
	var pane string
	c.Poll("a second pane in Herdr session "+s.Session, stepBound, func() (bool, string) {
		panes := herdrPanes(c, s)
		for p := range panes {
			if p != first.Pane {
				pane = p
			}
		}
		return len(panes) == 2 && pane != "", fmt.Sprintf("panes %v", panes)
	})
	term.Press(harness.Enter)
	pickEngine(term, "Claude")
	term.Press(harness.Enter)
	term.WaitFor("choose the cache")
	term.Press(harness.Enter)
	term.WaitFor("❯", 20*time.Second)
	sock := herdrSocket(s)
	var second Chat
	c.Poll("two live Claude chats on "+sock+", the second on "+pane, 20*time.Second, func() (bool, string) {
		rows, _ := liveChats(c, sock)
		seats := liveSeats(c, sock)
		seen := fmt.Sprintf("chats %v, seats %v", rows, seats)
		if len(rows) != 2 || len(seats) != 2 || seats[first.Session] != [2]string{first.Seat, first.Pane} {
			return false, seen
		}
		for session, seat := range seats {
			if session != first.Session && seat[1] == pane && rows[session][0] == "live-claude" {
				second = Chat{Session: session, Seat: seat[0], Pane: pane}
			}
		}
		return second.Session != "", seen
	})
	s.Chat = second
	second.Engine = engineOf(c, s)
	s.Chats = []Chat{first, second}
	cli := c.Terminal(s.Term + "-cli")
	cli.WaitFor(prompt)
	want := map[string]string{}
	for i, chat := range s.Chats {
		s.Chat, s.Name = chat, c.Unique(fmt.Sprintf("tab%d", i+1))
		nameFromCLI(c, cli, s, chat.Session)
		want[chat.Pane] = s.Name
		s.Chats[i].Name = s.Name
	}
	c.Poll("each tab under its chat's name", stepBound, func() (bool, string) {
		got := paneTabs(c, s)
		return fmt.Sprint(got) == fmt.Sprint(want), fmt.Sprintf("tabs %v, want %v", got, want)
	})
	s.Chat, s.Pane, s.Name = first, first.Pane, s.Chats[0].Name
	fleetConsistent(c, s)
	return s
}

// paneTabs maps each pane of the branch's Herdr session to the label of the tab it sits in.
func paneTabs(c *harness.Ctx, s State) map[string]string {
	var panes struct {
		Result struct {
			Panes []struct {
				PaneID string `json:"pane_id"`
				TabID  string `json:"tab_id"`
			} `json:"panes"`
		} `json:"result"`
	}
	var tabs struct {
		Result struct {
			Tabs []struct {
				TabID string `json:"tab_id"`
				Label string `json:"label"`
			} `json:"tabs"`
		} `json:"result"`
	}
	paneOut, tabOut := read(c, herdrCLI(s, "pane", "list")...), read(c, herdrCLI(s, "tab", "list")...)
	if err := json.Unmarshal([]byte(paneOut), &panes); err != nil {
		c.Failf("herdr pane list: %v:\n%s", err, paneOut)
	}
	if err := json.Unmarshal([]byte(tabOut), &tabs); err != nil {
		c.Failf("herdr tab list: %v:\n%s", err, tabOut)
	}
	label := map[string]string{}
	for _, tab := range tabs.Result.Tabs {
		label[tab.TabID] = tab.Label
	}
	got := map[string]string{}
	for _, pane := range panes.Result.Panes {
		got[pane.PaneID] = label[pane.TabID]
	}
	return got
}
