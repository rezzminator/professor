//go:build journey

package journey

import (
	"regexp"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// question: with the picker open in a terminal of its own and left untouched, the owner types a prompt
// that makes the engine ask, sees the question with its options, and answers it with Enter; Herdr and pfm
// read the chat blocked while it is open and idle after, and the idle picker, no key pressed in it, marks
// the chat's row blocked while the question is open and drops the mark once it is answered.
func question(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	picker := c.Terminal("question-picker")
	picker.WaitFor(prompt)
	picker.Type("pfm ls\r")
	picker.WaitFor("New chat", 20*time.Second)
	ask(c, s)
	picker.WaitScreen("the asking chat's row "+blockedRow.String(), stepBound, blockedRow.MatchString)
	term.Press(harness.Enter)
	term.WaitGone(questionHint, 20*time.Second)
	term.WaitFor("❯")
	awaitAttention(c, s, "idle", stepBound)
	picker.WaitScreen("the answered chat's row without the blocked mark", stepBound, func(screen string) bool {
		return askingRow.MatchString(screen) && !blockedRow.MatchString(screen)
	})
	picker.Press(harness.Esc)
	picker.WaitGone("New chat", 20*time.Second)
	return s
}

// askingRow is the asking chat's picker row, its work column before the model's ◇ whatever it shows.
var askingRow = regexp.MustCompile(`a question mock-engine: .*\s◇\s`)

// blockedRow is the asking chat's picker row with the blocked mark ⚠ in its work column, the column
// just before the model's ◇; a working chat shows its core ● or ◉ there.
var blockedRow = regexp.MustCompile(`a question mock-engine: .*\s⚠\s+◇\s`)

// questionHint is the footer of the fake engine's open question.
const questionHint = "Enter to confirm · Esc to cancel"

// ask types a prompt whose turn ends in a question and waits for it on screen and for Herdr and pfm
// to read the chat blocked.
func ask(c *harness.Ctx, s State) {
	term := c.Terminal(s.Term)
	option := c.Unique("answer")
	term.Type(`a question mock-engine: {"type":"menu","options":["` + option + `","no"],"selected":1}` + "\r")
	term.WaitFor("❯ 1. "+option, 20*time.Second)
	term.WaitFor(questionHint)
	awaitAttention(c, s, "blocked", stepBound)
}
