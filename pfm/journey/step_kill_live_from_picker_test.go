//go:build journey

package journey

import (
	"fmt"
	"strings"

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
	term.WaitFor("New chat")
	term.Type(s.Name)
	term.WaitScreen("the picker narrowed to "+s.Name, func(screen string) bool {
		return strings.Contains(screen, "find › "+s.Name)
	})
	// The find also lists other chats whose text holds the name's letters in order, above it or below.
	pickerSelect(term, "live chat "+s.Name, func(cells string) bool { return rowIs(cells, "● ", s.Name) })
	term.Press(harness.Ctrl('x'))
	c.Poll("the engine "+fmt.Sprint(s.Chat.Engine)+" of the killed chat gone", func() (bool, string) {
		_, alive := processes(c)[s.Chat.Engine]
		return !alive, fmt.Sprintf("engine %d alive", s.Chat.Engine)
	})
	c.Poll(s.Name+" killed in state and gone from pfm chat ls --all", func() (bool, string) {
		out := read(c, "pfm", "chat", "ls", "--all")
		hidden := strings.TrimSpace(
			stateSQL(c, "select count(*) from hidden where uuid = '"+s.Chat.Session+"'"),
		)
		return hidden == "1" && chatLsRow(out, s.Name) == nil, "killed rows " + hidden + ", listed:\n" + out
	})
	// The picker is still the pane's screen once the kill has landed: its find line holds the name, its
	// header names the hidden chat, and neither the killed row nor a shell prompt is drawn. Another
	// branch's live chat may stay listed: the find also matches a row whose name holds the query's
	// letters in order (a prompt naming a path under the run's scratch directory does).
	term.WaitScreen("the picker drawn without the killed row", func(screen string) bool {
		return strings.Contains(screen, "find › "+s.Name) && strings.Contains(screen, "hidden — "+s.Name) &&
			!pickerDrawsRow(screen, s.Name) && !strings.Contains(screen, prompt)
	})
	return s
}

// pickerDrawsRow reports whether a list row of the picker's screen, selected (`││› ● name`) or not
// (`│││ ● name`), is the chat named name.
func pickerDrawsRow(screen, name string) bool {
	for _, line := range strings.Split(screen, "\n") {
		_, row, found := strings.Cut(line, "││")
		if !found {
			continue
		}
		row = strings.TrimLeft(row, " │›") // the row's rail: a bar, or the cursor's chevron on the selected row
		if mark, _, marked := strings.Cut(row, " "); marked && rowIs(row, mark+" ", name) {
			return true
		}
	}
	return false
}
