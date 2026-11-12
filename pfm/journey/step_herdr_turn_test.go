//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// herdrTurn: the owner types a prompt whose turn runs a few seconds in the branch's Herdr chat; Herdr
// shows the pane working while the fake engine's busy footer is up and idle once the reply is under
// the prompt, pfm's status reads the same, and the status line under the box is the chat's own.
func herdrTurn(c *harness.Ctx, s State) State {
	reply := c.Unique("reply")
	term := c.Terminal(s.Term)
	term.Type(`a turn mock-engine: {"type":"turn","busy_ms":5000,"reply":"` + reply + `"}` + "\r")
	awaitAttention(c, s, "working", 5*time.Second)
	term.WaitFor("⏺ "+reply, 20*time.Second)
	awaitAttention(c, s, "idle", stepBound)
	requireStatusRow(c, s)
	return s
}

// reloadTypedDrawn is reloadTyped, then the reloaded chat's status line drawn under its box.
func reloadTypedDrawn(c *harness.Ctx, s State) State {
	s = reloadTyped(c, s)
	requireStatusRow(c, s)
	return s
}

// paneAttention is the agent status Herdr shows for the branch's pane ("" when the pane has none).
func paneAttention(c *harness.Ctx, s State) string {
	var list struct {
		Result struct {
			Panes []struct {
				PaneID string `json:"pane_id"`
				Status string `json:"agent_status"`
			} `json:"panes"`
		} `json:"result"`
	}
	out := read(c, herdrCLI(s, "pane", "list")...)
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		c.Failf("herdr pane list: %v:\n%s", err, out)
	}
	for _, p := range list.Result.Panes {
		if p.PaneID == s.Pane {
			return p.Status
		}
	}
	return ""
}

// chatAttention is the state `pfm chat status --json` reads for the branch's chat.
func chatAttention(c *harness.Ctx, s State) string {
	r := c.Read("pfm", "chat", "status", s.Chat.Session, "--json")
	var status struct {
		State string `json:"state"`
	}
	if r.Code != 0 || json.Unmarshal([]byte(r.Stdout), &status) != nil {
		return fmt.Sprintf("unread (exit %d: %s%s)", r.Code, r.Stdout, r.Stderr)
	}
	return status.State
}

// awaitAttention waits for Herdr's status of the branch's pane and pfm's state of its chat to both
// read want, and for the pane's tab to carry want's mark ahead of the chat's name; a blinking mark
// (blocked) must then show each of its frames within blinkBound, so a mark that never blinks fails.
func awaitAttention(c *harness.Ctx, s State, want string, within time.Duration) {
	name := ""
	c.Poll("Herdr and pfm read pane "+s.Pane+" "+want+", its tab marked "+strings.Join(tabMarks[want], "/"), within,
		func() (bool, string) {
			herdr, pfm, label := paneAttention(c, s), chatAttention(c, s), paneTabLabel(c, s)
			name = tabName(c, s)
			return herdr == want && pfm == want && labelFrame(label, want, name) != "",
				fmt.Sprintf("Herdr %q, pfm %q, tab %q", herdr, pfm, label)
		})
	seen := map[string]bool{}
	c.Poll("the tab of pane "+s.Pane+" shows each frame of "+strings.Join(tabMarks[want], "/"), blinkBound,
		func() (bool, string) {
			label := paneTabLabel(c, s)
			if frame := labelFrame(label, want, name); frame != "" {
				seen[frame] = true
			}
			return len(seen) == len(tabMarks[want]), fmt.Sprintf("tab %q, frames seen %v", label, seen)
		})
}

// blinkBound is how long a blinking mark gets to show each of its frames: a few of its half-second
// frames.
const blinkBound = 3 * time.Second

// holdAttention requires Herdr and pfm to keep reading want, and the pane's tab to keep a frame of
// want's mark ahead of the chat's name, over reads reads in a row, the first included: a read of
// anything else fails the step at once.
func holdAttention(c *harness.Ctx, s State, want string, reads int) {
	held := 0
	c.Poll(fmt.Sprintf("Herdr and pfm keep reading pane %s %s over %d reads", s.Pane, want, reads), stepBound,
		func() (bool, string) {
			herdr, pfm, label := paneAttention(c, s), chatAttention(c, s), paneTabLabel(c, s)
			if herdr != want || pfm != want || labelFrame(label, want, tabName(c, s)) == "" {
				c.Failf(
					"pane %s left %s after %d reads: Herdr %q, pfm %q, tab %q",
					s.Pane,
					want,
					held,
					herdr,
					pfm,
					label,
				)
			}
			held++
			return held >= reads, fmt.Sprintf("%d reads", held)
		})
}

// tabMarks are the marks pfm's Herdr painter draws ahead of a chat's tab name, by the chat's state: the
// default glyphs (pfm/internal/herdr/tab_glyphs.go defaultTabGlyphs) of the default
// chat.herdrTabStatus, emoji. Blocked and error blink between two frames, half a second each; every
// other state is one still frame.
var tabMarks = map[string][]string{
	"working": {"🔵"}, "idle": {"⚪"}, "blocked": {"🔴", "⭕"}, "done": {"🟡"},
	"error": {"🟥", "❌"}, "dead": {"⬛"}, "unknown": {"❔"},
}

// markedLabel is the tab label pfm paints for a chat named name in a still state: its mark, a space,
// the name.
func markedLabel(state, name string) string { return tabMarks[state][0] + " " + name }

// labelFrame is the frame of state's mark that label shows ahead of a space and name, "" when label is
// no frame of state's mark ahead of name.
func labelFrame(label, state, name string) string {
	for _, frame := range tabMarks[state] {
		if label == frame+" "+name {
			return frame
		}
	}
	return ""
}

// namedLabel says label is name, bare or under a frame of any state's mark.
func namedLabel(label, name string) bool {
	if label == name {
		return true
	}
	for state := range tabMarks {
		if labelFrame(label, state, name) != "" {
			return true
		}
	}
	return false
}

// tabNameRunes is the most of a chat's name a marked tab label shows: Herdr's 80-rune title less the
// mark and its space (pfm/internal/herdr DecoratedTabLabel).
const tabNameRunes = 78

// tabName is the name the branch's chat's tab carries: the name a step gave it, else pfm's row name (an
// unnamed chat's first prompt), cut to tabNameRunes.
func tabName(c *harness.Ctx, s State) string {
	name := s.Name
	if name == "" {
		name = lsName(c, s)
	}
	if runes := []rune(name); len(runes) > tabNameRunes {
		name = strings.TrimRight(string(runes[:tabNameRunes]), " ")
	}
	return name
}

// awaitTopTab waits for Herdr's tab bar, the screen's top line, to draw a tab labelled state's mark and
// name.
func awaitTopTab(term *harness.Terminal, state, name string) {
	label := markedLabel(state, name)
	term.WaitScreen("a Herdr tab labelled "+label, stepBound, func(screen string) bool {
		top, _, _ := strings.Cut(screen, "\n")
		return strings.Contains(" "+strings.Join(strings.Fields(top), " ")+" ", " "+label+" ")
	})
}

// boxRule is the fake Claude's input-box rule (pfm/internal/mockengine/pane.go claudeComposerRule).
const boxRule = "────────────────────────────────────────"

// statusRow is the row under the last input-box rule on screen, from the rule's column: the line the
// fake Claude draws from pfm's statusLine command. found is false when no box is on screen.
func statusRow(screen string) (row string, found bool) {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 2; i >= 0; i-- {
		col := strings.Index(lines[i], boxRule)
		if col < 0 {
			continue
		}
		runes, left := []rune(lines[i+1]), utf8.RuneCountInString(lines[i][:col])
		if len(runes) <= left {
			return "", true
		}
		return strings.TrimRight(string(runes[left:]), " "), true
	}
	return "", false
}

// fleetCounts is the status line's closing fleet counts: every branch's chats move them, and the
// glyph steps · ✦ ✹ with the count (sky.SnapshotCounts), so a step pins only their shape.
const fleetCounts = ` [·✦✹]\d* [·✦✹]\d*$`

// requireStatusRow waits for the branch's chat to draw its status line under the box: the account's
// medal, the model and the project exactly, then the fleet's counts by shape; an ERR segment or a blank
// row never matches.
func requireStatusRow(c *harness.Ctx, s State) {
	want := regexp.MustCompile(`^🥇 ◇ Sonnet·effort unknown │ ` + regexp.QuoteMeta(filepath.Base(s.Project)) +
		` │` + fleetCounts)
	c.Terminal(s.Term).WaitScreen("the status line "+want.String()+" under the box", stepBound,
		func(screen string) bool {
			row, found := statusRow(screen)
			return found && want.MatchString(row)
		})
}
