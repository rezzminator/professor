//go:build journey

package journey

import (
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// killLiveFromPicker: the owner names the branch's live chat from a second terminal, opens a new Herdr tab
// with the new-tab key (prefix c), finds the chat in its picker by name and presses Ctrl-X; the row leaves
// and the picker stays drawn, the chat's engine is gone, it is killed in state and `pfm chat ls --all`
// lists it no more.
func killLiveFromPicker(c *harness.Ctx, s State) State {
	cli := c.Terminal(s.Term + "-cli")
	cli.WaitFor(prompt)
	s.Name = c.Unique("attention")
	nameFromCLI(c, cli, s, s.Chat.Session)
	term := c.Terminal(s.Term)
	term.Press(harness.Ctrl('b'), harness.Key("c"))
	term.WaitFor("new tab")
	term.Press(harness.Enter)
	term.WaitFor("New chat", 20*time.Second)
	term.Type(s.Name)
	term.WaitScreen("the picker narrowed to "+s.Name, stepBound, func(screen string) bool {
		return strings.Contains(screen, "find › "+s.Name)
	})
	if !rowIs(selectedRow(term.Screen()), "● ", s.Name) {
		term.Press(harness.Down)
	}
	term.WaitScreen("the live chat "+s.Name+" selected", stepBound, func(screen string) bool {
		return rowIs(selectedRow(screen), "● ", s.Name)
	})
	term.Press(harness.Ctrl('x'))
	c.Poll("the engine "+fmt.Sprint(s.Chat.Engine)+" of the killed chat gone", stepBound, func() (bool, string) {
		_, alive := processes(c)[s.Chat.Engine]
		return !alive, fmt.Sprintf("engine %d alive", s.Chat.Engine)
	})
	c.Poll(s.Name+" killed in state and gone from pfm chat ls --all", stepBound, func() (bool, string) {
		out := read(c, "pfm", "chat", "ls", "--all")
		hidden := strings.TrimSpace(
			read(c, "sqlite3", stateDB(), "select count(*) from hidden where uuid = '"+s.Chat.Session+"'"),
		)
		return hidden == "1" && chatLsRow(out, s.Name) == nil, "killed rows " + hidden + ", listed:\n" + out
	})
	// The picker is still the pane's screen once the kill has landed: its find line holds the name, and
	// no row and no shell prompt is drawn.
	term.WaitScreen("the picker drawn without the killed row", stepBound, func(screen string) bool {
		return strings.Contains(screen, "find › "+s.Name) && strings.Contains(screen, "no matches for “"+s.Name+"”") &&
			!strings.Contains(screen, prompt)
	})
	return s
}
