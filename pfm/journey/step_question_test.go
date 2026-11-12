//go:build journey

package journey

import (
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// question: the owner types a prompt that makes the engine ask, sees the question with its options,
// and answers it with Enter; Herdr and pfm read the chat blocked while it is open and idle after.
func question(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	ask(c, s)
	term.Press(harness.Enter)
	term.WaitGone(questionHint, 20*time.Second)
	term.WaitFor("❯")
	awaitAttention(c, s, "idle", stepBound)
	return s
}

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
