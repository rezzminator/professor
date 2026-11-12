//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// The guards' deny texts, as pfm/internal/hookentry writes them: git-guard's route after its blocked
// line (git_guard.go gitGuardOnlyGitter), pcm-guard's line for a path only pcm writes (pcm_guard.go
// pcmGuardRoute with pcmGuardPcmTail).
const (
	gitGuardRoute = `Only gitter writes this: spawn Agent(subagent_type: "gitter") with the repo path and the exact ` +
		"change. Never retry, rephrase, split or wrap the denied command to get past the guard."
	pcmGuardLine = `pcm-guard: Only pcm writes %s: spawn Agent(subagent_type: "pcm") with the exact change; a ` +
		"sub-agent returns the change as a proposal; pcm is machine-global: no pcm registered means this " +
		"host's install is stale → run pfm install --yes."
)

// guardsProject: in the first pane of the branch's Herdr session the owner leaves the picker for the
// pane's shell, makes a git project of the run's own under $HOME/work (one commit, one untracked
// draft) and opens pfm's picker in it, which offers a new chat there, selected.
func guardsProject(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	term.Press(harness.Esc)
	term.WaitFor(prompt, 20*time.Second)
	name := c.Unique("guards")
	s.Project = filepath.Join(s.Home, "work", name)
	term.Type("mkdir -p $HOME/work/" + name + " && cd $HOME/work/" + name + " && git init -q && " +
		"printf '# guards\\n' > README.md && git add README.md && " +
		"git -c user.name=owner -c user.email=owner@journey.invalid commit -q -m 'the guards project' && " +
		"printf 'draft\\n' > draft.md && pfm\r")
	newChatSelectedIn(term, name)
	return s
}

// newChatSelected moves the picker's cursor to its new-chat row, as the owner does before Enter: the picker
// opens on it unless a chat is pinned above it (one whose schedule fired unseen, from any branch of the day),
// and then he steps down to it. A row is told from the next by its mark and name, the cells before its
// first gap: its age cell ticks every second.
func newChatSelected(term *harness.Terminal) {
	const row = "✦ New chat → "
	term.WaitFor(row, 20*time.Second)
	selected := func(screen string) string {
		cells, _, _ := strings.Cut(selectedRow(screen), "  ")
		return cells
	}
	var walked []string
	for range 8 {
		// Two polls reading the same row: a frame caught while the pane redraws shows a row half drawn.
		var on, last string
		term.WaitScreen("the picker's cursor resting on a row", stepBound, func(screen string) bool {
			last, on = on, selected(screen)
			return on != "" && on == last
		})
		if strings.HasPrefix(on, row) {
			return
		}
		walked = append(walked, on)
		term.Press(harness.Down)
		term.WaitScreen("the picker's cursor off "+on, stepBound, func(screen string) bool {
			now := selected(screen)
			return now != "" && now != on
		})
	}
	term.WaitScreen(fmt.Sprintf("the picker's new chat selected (the cursor walked %q)", walked), stepBound,
		func(screen string) bool { return strings.HasPrefix(selectedRow(screen), row) })
}

// newChatSelectedIn waits for the picker to select the new chat of the group of project name. The pane
// is narrow: the row cuts the project's name short, its group's header holds it whole.
func newChatSelectedIn(term *harness.Terminal, name string) {
	newChatSelected(term)
	term.WaitScreen("the picker's new chat, selected, in the group of "+name, 20*time.Second,
		func(screen string) bool {
			return strings.Contains(screen, "╭─ "+name+" ─") && strings.Contains(screen, "› ✦ New chat → ")
		})
}

// gitWriteRefused: the owner's turn has the chat run `git commit` in the project; git-guard refuses it
// before it runs. The pane shows the deny, the chat's tool result is git-guard's whole reason, the
// project's HEAD is where it was, and pfm's activity log holds the hook's deny with that reason.
func gitWriteRefused(c *harness.Ctx, s State) State {
	head := read(c, "git", "-C", s.Project, "rev-parse", "HEAD")
	command := "git commit --allow-empty -m " + c.Unique("journey")
	reason := "git-guard blocked `" + command + "`.\n" + gitGuardRoute
	rows := guardTurn(c, s, "commit", "Bash", map[string]string{"command": command})
	wantRow(c, rows, "⏺ Bash — denied by hook: git-guard blocked `git commit")
	toolOutcome(c, s, "command", command, reason, true)
	if after := read(c, "git", "-C", s.Project, "rev-parse", "HEAD"); after != head {
		c.Failf("the refused commit moved %s's HEAD from %q to %q", s.Project, head, after)
	}
	hookDenied(c, "git-guard", reason)
	return s
}

// gitReadPasses: the owner's turn has the chat run `git status --short`; no guard refuses it, the pane
// shows the call, and the chat's tool result is the project's status: its one untracked draft.
func gitReadPasses(c *harness.Ctx, s State) State {
	command := "git status --short"
	rows := guardTurn(c, s, "status", "Bash", map[string]string{"command": command})
	wantRow(c, rows, "⏺ Bash(…)")
	noDeny(c, rows)
	toolOutcome(c, s, "command", command, "?? draft.md\n", false)
	return s
}

// frameworkWriteRefused: the owner's turn has the chat write a rule under the project's .claude/;
// pcm-guard refuses it. The chat made the call (its tool result is pcm-guard's whole reason, and the
// activity log holds the hook's deny with it), and the file is not there.
func frameworkWriteRefused(c *harness.Ctx, s State) State {
	path := filepath.Join(s.Project, ".claude", "rules", "journey.md")
	reason := fmt.Sprintf(pcmGuardLine, path)
	rows := guardTurn(c, s, "rule", "Write", map[string]string{"file_path": path, "content": "# journey\n"})
	wantRow(c, rows, "⏺ Write — denied by hook: pcm-guard: Only pcm writes")
	toolOutcome(c, s, "file_path", path, reason, true)
	hookDenied(c, "pcm-guard", reason)
	if r := c.Read("test", "-e", path); r.Code != 1 {
		c.Failf("test -e %s exited %d, want 1: the refused write left the file", path, r.Code)
	}
	return s
}

// ordinaryWritePasses: the owner's turn has the chat write notes.md, whose text names a framework
// path; no guard refuses it, the pane shows the call, and the file holds exactly the text written.
func ordinaryWritePasses(c *harness.Ctx, s State) State {
	path := filepath.Join(s.Project, "notes.md")
	text := "the rules live in .claude/rules/journey.md\n"
	rows := guardTurn(c, s, "notes", "Write", map[string]string{"file_path": path, "content": text})
	wantRow(c, rows, "⏺ Write(…)")
	noDeny(c, rows)
	toolOutcome(c, s, "file_path", path, "File created successfully at: "+path, false)
	if got := read(c, "cat", path); got != text {
		c.Failf("%s holds %q, want %q", path, got, text)
	}
	return s
}

// proseWritePasses: the owner's turn has the chat write a file from a heredoc whose data names a
// framework path; pcm-guard judges the heredoc as data, not a write to that path, so the call runs
// and the file holds exactly the heredoc's text.
func proseWritePasses(c *harness.Ctx, s State) State {
	command := "cat > prose.md <<'EOF'\nsee .claude/rules/journey.md and CLAUDE.md\nEOF"
	rows := guardTurn(c, s, "prose", "Bash", map[string]string{"command": command})
	wantRow(c, rows, "⏺ Bash(…)")
	noDeny(c, rows)
	toolOutcome(c, s, "command", command, "", false)
	path := filepath.Join(s.Project, "prose.md")
	if got, want := read(c, "cat", path), "see .claude/rules/journey.md and CLAUDE.md\n"; got != want {
		c.Failf("%s holds %q, want %q", path, got, want)
	}
	return s
}

// guardTurn types one turn whose scripted step is the tool call, performed by the fake engine as
// Claude would unless a hook denies it, then a reply; it returns the pane's rows from the typed
// prompt on, so a row of an earlier turn is never read as this one's.
func guardTurn(c *harness.Ctx, s State, label, tool string, input map[string]string) []string {
	words, reply := c.Unique(label), c.Unique("reply")
	steps := []map[string]any{
		{"type": "tool_call", "tool": tool, "input": input, "perform": true},
		{"type": "turn", "reply": reply},
	}
	directive, err := json.Marshal(steps)
	if err != nil {
		c.Failf("encode the turn's steps %v: %v", steps, err)
	}
	typedTurn(c, s, words, string(directive), reply)
	screen := c.Terminal(s.Term).Screen()
	at := strings.LastIndex(screen, words+" mock-engine: ")
	if at < 0 {
		c.Failf("the pane no longer shows the typed prompt %q:\n%s", words, screen)
	}
	return strings.Split(screen[at:], "\n")
}

// wantRow fails the step unless one of rows shows want, the pane's border and padding around it aside.
func wantRow(c *harness.Ctx, rows []string, want string) {
	for _, row := range rows {
		if strings.Contains(row, want) {
			return
		}
	}
	c.Failf("the pane shows no row %q after the typed prompt:\n%s", want, strings.Join(rows, "\n"))
}

// noDeny fails the step when a row after the typed prompt shows a hook's deny.
func noDeny(c *harness.Ctx, rows []string) {
	for _, row := range rows {
		if strings.Contains(row, "denied by hook") {
			c.Failf("the pane shows a deny after the typed prompt: %q\n%s", row, strings.Join(rows, "\n"))
		}
	}
}

// toolOutcome fails the step unless the chat's transcript holds one tool call whose input's key is
// value, with a tool result of exactly content and isError.
func toolOutcome(c *harness.Ctx, s State, key, value, content string, isError bool) {
	type part struct {
		Type      string         `json:"type"`
		ID        string         `json:"id"`
		Input     map[string]any `json:"input"`
		ToolUseID string         `json:"tool_use_id"`
		Content   string         `json:"content"`
		IsError   bool           `json:"is_error"`
	}
	path := transcriptOf(c, s)
	var uses []string
	results := map[string]part{}
	for _, line := range strings.Split(read(c, "cat", path), "\n") {
		var record struct {
			Message struct {
				Content []part `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &record) != nil {
			continue // a record whose content is text carries no tool call
		}
		for _, p := range record.Message.Content {
			if p.Type == "tool_use" && p.Input[key] == value {
				uses = append(uses, p.ID)
			}
			if p.Type == "tool_result" {
				results[p.ToolUseID] = p
			}
		}
	}
	if len(uses) != 1 {
		c.Failf("%s holds %d tool calls with %s %q, want 1", path, len(uses), key, value)
	}
	got, found := results[uses[0]]
	if !found || got.Content != content || got.IsError != isError {
		c.Failf("%s's tool result for %s %q = %q (is_error %t, found %t), want %q (is_error %t)",
			path, key, value, got.Content, got.IsError, found, content, isError)
	}
}

// transcriptOf is the path of the branch's chat's transcript, the one file of its session id under
// the owner's Claude config directories, links resolved.
func transcriptOf(c *harness.Ctx, s State) string {
	out := read(c, "sh", "-c", `for f in "$HOME"/.claude/projects/*/"$1".jsonl "$HOME"/.cc/*/projects/*/"$1".jsonl; `+
		`do [ -f "$f" ] && readlink -f "$f"; done | sort -u`, "sh", s.Chat.Session)
	paths := strings.Fields(out)
	if len(paths) != 1 {
		c.Failf("chat %s has transcripts %q, want one", s.Chat.Session, paths)
	}
	return paths[0]
}

// hookDenied waits for pfm's activity log to hold, since the step began, the hooks.run record of hook
// denying with exactly reason; the reason names what the branch typed, so no other branch's matches.
func hookDenied(c *harness.Ctx, hook, reason string) {
	from, _ := c.Window()
	c.Poll(hook+"'s deny in pfm's activity log", stepBound, func() (bool, string) {
		var seen []string
		for _, line := range activity(c, from, time.Time{}) {
			var record struct {
				Msg      string `json:"msg"`
				Hook     string `json:"hook"`
				Decision string `json:"decision"`
				Reason   string `json:"reason"`
			}
			if json.Unmarshal([]byte(line), &record) != nil || record.Msg != "hooks.run" || record.Hook != hook {
				continue
			}
			if record.Decision == "deny" && record.Reason == reason {
				return true, ""
			}
			seen = append(seen, line)
		}
		return false, fmt.Sprintf("its records: %q", seen)
	})
}
