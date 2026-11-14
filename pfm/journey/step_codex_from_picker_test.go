//go:build journey

package journey

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// codexFromPicker: in the first pane of the branch's Herdr session the owner leaves the picker and asks
// pfm for a Codex chat on a model Codex does not offer: the refusal is printed, the exit is non-zero and
// nothing is launched. Then the owner opens the picker again, moves the engine chip to Codex and walks the
// wizard with Enter; the fake Codex draws in the pane and pfm lists exactly one live Codex chat on the
// session's socket, its seat live on that pane, its engine under the session's Herdr server. He names the
// chat with Codex's own /rename: pfm's row, the Herdr tab and the seat all take the name.
func codexFromPicker(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	term.Press(harness.Esc)
	term.WaitFor(prompt)
	model := c.Unique("no-such-model")
	command := "pfm chat new --engine cx --name " + c.Unique("refused") + " --model " + model
	if code := paneRun(term, "refused", command, 20*time.Second); code == 0 {
		c.Failf("pfm chat new --model %s exited 0, want a refusal", model)
	}
	term.WaitFor(`codex model "` + model + `" is not offered by Codex`)
	if panes := herdrPanes(c, s); len(panes) != 1 {
		c.Failf("Herdr lists panes %v after the refused launch, want only %s", panes, s.Pane)
	}
	if seats := liveSeats(c, herdrSocket(s)); len(seats) != 0 {
		c.Failf("live seats %v on %s after the refused launch, want none", seats, herdrSocket(s))
	}
	term.Type("pfm\r")
	newChatSelected(term)
	term.Press(harness.Enter)
	pickEngine(term, "Codex")
	term.Press(harness.Enter)
	term.WaitFor("Ask Codex to do anything", 20*time.Second)
	s.Chat = oneLiveChat(c, s, "live-codex")
	s.Chat.Engine = engineOf(c, s)
	s.Name = c.Unique("codex-title")
	term.Type("/rename " + s.Name + "\r")
	namesAgree(c, s, "codex /rename")
	fleetConsistent(c, s)
	return s
}

// engineChip is a boxed chip on the screen: ◖ {short} ◗.
var engineChip = regexp.MustCompile(`◖ (\S+) ◗`)

// pickEngine waits for the wizard's engine step and moves its chip to the engine named short, as the
// owner does with the arrow keys, reading the boxed chip (◖ {short} ◗) on the screen. Each press waits
// for the boxed chip itself to move before the next read: a pane caught while it redraws differs from
// the screen before the press with the chip row still undrawn, and a press sent on that reading lands
// after the chip was read as right and moves it on.
func pickEngine(term *harness.Terminal, short string) {
	term.WaitFor("choose the engine", 20*time.Second)
	boxed := func(screen string) string {
		return strings.Join(engineChip.FindAllString(screen, -1), " ")
	}
	chip := "◖ " + short + " ◗"
	var on string
	rest := func(what, was string) {
		var last string
		term.WaitScreen(what, stepBound, func(screen string) bool {
			last, on = on, boxed(screen)
			return on != "" && on != was && on == last
		})
	}
	rest("the engine step's boxed chip", "")
	for range 4 {
		if strings.Contains(on, chip) {
			return
		}
		was := on
		term.Press(harness.Right)
		rest("the boxed chip to leave "+was, was)
	}
	term.WaitFor(chip)
}

// oneLiveChat waits for exactly one live chat of kind on the branch's socket, its seat live on the
// branch's pane, and returns it.
func oneLiveChat(c *harness.Ctx, s State, kind string) Chat {
	var chat Chat
	c.Poll("one "+kind+" chat with a live seat on "+s.Pane, 20*time.Second, func() (bool, string) {
		rows, _ := liveChats(c, herdrSocket(s))
		seats := liveSeats(c, herdrSocket(s))
		seen := fmt.Sprintf("chats %v, seats %v", rows, seats)
		if len(rows) != 1 || len(seats) != 1 {
			return false, seen
		}
		for session, row := range rows {
			seat, ok := seats[session]
			if !ok || row != [2]string{kind, herdrSocket(s)} || seat[1] != s.Pane {
				return false, seen
			}
			chat = Chat{Session: session, Seat: seat[0]}
		}
		return true, seen
	})
	return chat
}

// paneRun is shellRun for a shell inside a Herdr pane, whose lines Herdr's frame indents: the exit
// line is found anywhere on a row.
func paneRun(term *harness.Terminal, label, command string, within time.Duration) int {
	term.Type(command + `; echo "` + label + ` exit=$?"` + "\r")
	marker := regexp.MustCompile(regexp.QuoteMeta(label) + ` exit=(\d+)`)
	var code int
	term.WaitScreen(label+"'s exit line", within, func(screen string) bool {
		m := marker.FindStringSubmatch(screen)
		if m != nil {
			code, _ = strconv.Atoi(m[1])
		}
		return m != nil
	})
	return code
}
