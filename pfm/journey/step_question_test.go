//go:build journey

package journey

import (
	"regexp"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// question: with the picker open in a terminal of its own and left untouched, the owner types a prompt
// that makes the engine ask, sees the question with its options, and answers it with Enter; Herdr and pfm
// read the chat blocked while it is open and idle after, and the idle picker, no key pressed in it, marks
// the chat's row blocked while the question is open and drops the mark once it is answered. Then he
// rejects a tool at its permission dialog (rejectTool).
func question(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	picker := c.Terminal("question-picker")
	picker.WaitFor(prompt)
	picker.Type("pfm ls\r")
	picker.WaitFor("New chat")
	ask(c, s)
	picker.WaitScreen("the asking chat's row "+blockedRow.String(), blockedRow.MatchString)
	term.Press(harness.Enter)
	term.WaitGone(questionHint)
	term.WaitFor("❯")
	awaitAttention(c, s, "idle")
	picker.WaitScreen("the answered chat's row without the blocked mark", func(screen string) bool {
		return askingRow.MatchString(screen) && !blockedRow.MatchString(screen)
	})
	picker.Press(harness.Esc)
	picker.WaitGone("New chat")
	rejectTool(c, s)
	return s
}

// rejectTool: the owner types a prompt whose turn asks to run a tool, sees Claude's permission dialog and
// presses Esc. Claude writes the rejection and fires no hook for it; Herdr and pfm read the chat blocked
// while the dialog is open and idle once it is rejected, and pfm reads the turn aborted.
func rejectTool(c *harness.Ctx, s State) {
	term := c.Terminal(s.Term)
	term.Type(`a tool mock-engine: {"type":"permission","tool":"Bash","input":{"command":"true"}}` + "\r")
	term.WaitFor("❯ 1. Yes")
	awaitAttention(c, s, "blocked")
	term.Press(harness.Esc)
	term.WaitFor("Interrupted · What should Claude do instead?")
	awaitAttention(c, s, "idle")
	aborted := entry{Role: "assistant", Text: "[turn aborted: interrupted]"}
	if got := entries(c, s.Chat.Session); len(got) == 0 || got[len(got)-1] != aborted {
		c.Failf("pfm chat read %s after the rejected tool: last entry of %v, want %v", s.Chat.Session, got, aborted)
	}
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
	term.WaitFor("❯ 1. " + option)
	term.WaitFor(questionHint)
	awaitAttention(c, s, "blocked")
}
