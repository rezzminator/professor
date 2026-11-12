//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// opencodeNew: in the first pane of the branch's Herdr session the owner leaves the picker for the shell
// and types `pfm chat new --engine ox --name {name}`; the fake OpenCode draws in a new tab, and from its
// first paint that tab carries the chat's title, the owner's name (escape chat-birth/new-from-cli,
// OpenCode tab), then the idle mark ahead of it once the chat is at rest.
func opencodeNew(c *harness.Ctx, s State) State {
	s = chatNewInPane(c, s, "ox", "live-opencode")
	c.Poll("OpenCode chat "+s.Chat.Session+" at rest, its tab "+markedLabel("idle", s.Name), stepBound,
		func() (bool, string) {
			label := paneTabLabel(c, s)
			if !namedLabel(label, s.Name) {
				c.Failf("OpenCode chat %s: its Herdr tab label %q, want %q under any state's mark", s.Chat.Session,
					label, s.Name)
			}
			return label == markedLabel("idle", s.Name), "tab " + label
		})
	fleetConsistent(c, s)
	return s
}

// opencodeRename: OpenCode's start report binds the chat's seat to OpenCode's own session (ses_…); the
// owner then retitles the session inside OpenCode (`/rename {name}`, before any turn), and pfm's Herdr
// plugin's title report names the chat's pfm row, its Herdr tab and its seat at once.
func opencodeRename(c *harness.Ctx, s State) State {
	c.Poll("OpenCode chat's seat "+s.Chat.Seat+" bound to an OpenCode session", stepBound, func() (bool, string) {
		seats := liveSeats(c, herdrSocket(s))
		for session, seat := range seats {
			if len(seats) == 1 && strings.HasPrefix(session, "ses_") && seat == [2]string{s.Chat.Seat, s.Pane} {
				s.Chat.Session = session
				return true, ""
			}
		}
		return false, fmt.Sprintf("live seats %v", seats)
	})
	s.Name = c.Unique("retitled")
	c.Terminal(s.Term).Type("/rename " + s.Name + "\r")
	c.Poll("OpenCode chat "+s.Chat.Session+": row, tab and seat named "+s.Name, stepBound, func() (bool, string) {
		seat := strings.TrimSpace(read(c, "sqlite3", stateDB(),
			"select coalesce(name, '') from seat where state = 'live' and session_id = '"+s.Chat.Session+"'"))
		got := [3]string{lsName(c, s), paneTabLabel(c, s), seat}
		return got == [3]string{s.Name, markedLabel("idle", s.Name), s.Name}, fmt.Sprintf("row, tab, seat = %q", got)
	})
	fleetConsistent(c, s)
	return s
}

// opencodeEnd: the owner quits OpenCode; its chat, seat and engine are gone from the branch's socket.
func opencodeEnd(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	term.Type("/exit\r")
	c.Poll("OpenCode chat "+s.Chat.Session+" gone from the chats, the seats and the engines", 20*time.Second,
		func() (bool, string) {
			ls := c.Read("pfm", "ls", "--tsv")
			seats := liveSeats(c, herdrSocket(s))
			_, engine := processes(c)[s.Chat.Engine]
			if ls.Code != 0 {
				// Between OpenCode's pane closing and its seat leaving live, pfm ls exits 1 for the whole
				// listing (builder B's fleet-reader defect, gather/herdrprobe.go): a state the poll waits out.
				return false, ls.Stderr
			}
			rows, _ := liveChats(c, herdrSocket(s))
			return len(rows) == 0 && len(seats) == 0 && !engine,
				fmt.Sprintf("chats %v, seats %v, engine %d alive %v", rows, seats, s.Chat.Engine, engine)
		})
	fleetConsistent(c, s)
	return s
}

// paneTabLabel is the label of the Herdr tab holding the branch's pane.
func paneTabLabel(c *harness.Ctx, s State) string {
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
	for _, pane := range panes.Result.Panes {
		for _, tab := range tabs.Result.Tabs {
			if pane.PaneID == s.Pane && tab.TabID == pane.TabID {
				return tab.Label
			}
		}
	}
	return fmt.Sprintf("(no tab of pane %s in %s %s)", s.Pane, paneOut, tabOut)
}
