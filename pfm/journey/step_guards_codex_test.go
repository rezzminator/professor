//go:build journey

package journey

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// engine-guard's ban lines, as pfm/internal/hookentry/engine_guard.go writes them after a git write
// (engineGuardGitBan) and after a framework write (engineGuardFileBan).
const (
	engineGitBan = "Codex and OpenCode never write git: only Claude does. Git reads (status, log, diff, show, " +
		"blame, ls-files) stay open; return the change to the user."
	engineFileBan = "Codex and OpenCode never change framework files (any CLAUDE.md or AGENTS.md, .claude/, " +
		".codex/, .opencode/, .git/, the Claude, Codex and OpenCode config dirs, the Professor clone's " +
		"templates/): only Claude does. Return the exact change to the user."
)

// codexInProject: the owner leaves the picker for its shell, which `end` may have reopened in a new pane
// in the home directory, goes back into the project, opens the picker on the project's new chat and
// walks the wizard on Codex's chip: the fake Codex draws in the session's one pane and pfm lists exactly
// one live Codex chat on the session's socket, its seat live on that pane.
func codexInProject(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	term.Press(harness.Esc)
	term.WaitFor(prompt, 20*time.Second)
	term.Type("cd " + s.Project + " && pfm\r")
	newChatSelectedIn(term, filepath.Base(s.Project))
	term.Press(harness.Enter)
	pickEngine(term, "Codex")
	term.Press(harness.Enter)
	term.WaitFor("Ask Codex to do anything", 20*time.Second)
	panes := herdrPanes(c, s)
	if len(panes) != 1 {
		c.Failf("Herdr lists panes %v with the Codex chat open, want one", panes)
	}
	for pane := range panes {
		s.Pane = pane
	}
	s.Chat = oneLiveChat(c, s, "live-codex")
	s.Chat.Engine = engineOf(c, s)
	fleetConsistent(c, s)
	return s
}

// codexGitWriteRefused: the owner's turn has the Codex chat run `git commit` in the project; the
// engine-guard pfm install wired into Codex's hooks.json (and trusted) refuses it before it runs. The
// pane shows the deny, the call's output in the thread's rollout is the guard's whole reason, the
// project's HEAD is where it was, and pfm's activity log holds the hook's deny with that reason.
func codexGitWriteRefused(c *harness.Ctx, s State) State {
	head := read(c, "git", "-C", s.Project, "rev-parse", "HEAD")
	command := "git commit --allow-empty -m " + c.Unique("journey")
	reason := "git-guard blocked `" + command + "`.\n" + engineGitBan
	rows := guardTurn(c, s, "commit", "Bash", map[string]string{"command": command})
	wantRow(c, rows, "⏺ Bash — denied by hook: git-guard blocked `git commit")
	codexToolOutput(c, s, command, reason)
	if after := read(c, "git", "-C", s.Project, "rev-parse", "HEAD"); after != head {
		c.Failf("the refused commit moved %s's HEAD from %q to %q", s.Project, head, after)
	}
	hookDenied(c, "engine-guard", reason)
	return s
}

// codexGitReadPasses: the owner's turn has the Codex chat run `git status --short`; the engine-guard
// lets the read run, the pane shows the call, and its output is the project's status as git reads it.
func codexGitReadPasses(c *harness.Ctx, s State) State {
	command := "git status --short"
	rows := guardTurn(c, s, "status", "Bash", map[string]string{"command": command})
	wantRow(c, rows, "⏺ Bash(…)")
	noDeny(c, rows)
	codexToolOutput(c, s, command, read(c, "git", "-C", s.Project, "status", "--short"))
	return s
}

// codexFrameworkWriteRefused: the owner's turn has the Codex chat write the project's AGENTS.md from its
// shell; the engine-guard refuses the write, the call's output is its whole reason, and no AGENTS.md is
// there.
func codexFrameworkWriteRefused(c *harness.Ctx, s State) State {
	path := filepath.Join(s.Project, "AGENTS.md")
	command := "printf '# codex\\n' > AGENTS.md"
	reason := "engine-guard blocked a write to " + path + ". " + engineFileBan
	rows := guardTurn(c, s, "agents", "Bash", map[string]string{"command": command})
	wantRow(c, rows, "⏺ Bash — denied by hook: engine-guard blocked a write to")
	codexToolOutput(c, s, command, reason)
	hookDenied(c, "engine-guard", reason)
	if r := c.Read("test", "-e", path); r.Code != 1 {
		c.Failf("test -e %s exited %d, want 1: the refused write left the file", path, r.Code)
	}
	return s
}

// codexHeredocScriptPasses: the owner's turn has the Codex chat write two scripts from heredocs, a
// greeter taking its name as $1 and a runner calling the greeter by its own directory, and run the
// greeter in the same command; the engine-guard judges the greeter from its heredoc body, so the call
// runs and greets.
func codexHeredocScriptPasses(c *harness.Ctx, s State) State {
	command := "cat > greet.sh <<'EOF'\nprintf 'hello %s\\n' \"$1\"\nEOF\n" +
		"cat > run.sh <<'EOF'\nsh \"$(dirname \"$0\")/greet.sh\" \"$1\"\nEOF\nsh greet.sh codex"
	rows := guardTurn(c, s, "heredoc", "Bash", map[string]string{"command": command})
	wantRow(c, rows, "⏺ Bash(…)")
	noDeny(c, rows)
	codexToolOutput(c, s, command, "hello codex\n")
	return s
}

// codexProjectScriptPasses: the owner's turn has the Codex chat run the project's runner script with an
// argument; the runner reaches its sibling greeter through its own directory, the engine-guard reads
// both from disk and lets the call run, and the greeting carries the argument.
func codexProjectScriptPasses(c *harness.Ctx, s State) State {
	command := "sh run.sh owner"
	rows := guardTurn(c, s, "runner", "Bash", map[string]string{"command": command})
	wantRow(c, rows, "⏺ Bash(…)")
	noDeny(c, rows)
	codexToolOutput(c, s, command, "hello owner\n")
	return s
}

// codexToolOutput fails the step unless the Codex thread's rollout holds one function call whose command
// is command, with an output of exactly output.
func codexToolOutput(c *harness.Ctx, s State, command, output string) {
	out := read(c, "sh", "-c", `for f in "$HOME"/.codex/sessions/*/*/*/rollout-*-"$1".jsonl; `+
		`do [ -f "$f" ] && readlink -f "$f"; done | sort -u`, "sh", s.Chat.Session)
	paths := strings.Fields(out)
	if len(paths) != 1 {
		c.Failf("thread %s has rollouts %q, want one", s.Chat.Session, paths)
	}
	var calls []string
	outputs := map[string]string{}
	for _, line := range strings.Split(read(c, "cat", paths[0]), "\n") {
		var record struct {
			Payload struct {
				Type      string `json:"type"`
				Arguments string `json:"arguments"`
				CallID    string `json:"call_id"`
				Output    string `json:"output"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &record) != nil {
			continue
		}
		var input struct {
			Command string `json:"command"`
		}
		switch record.Payload.Type {
		case "function_call":
			if json.Unmarshal([]byte(record.Payload.Arguments), &input) == nil && input.Command == command {
				calls = append(calls, record.Payload.CallID)
			}
		case "function_call_output":
			outputs[record.Payload.CallID] = record.Payload.Output
		}
	}
	if len(calls) != 1 {
		c.Failf("%s holds %d calls of %q, want 1", paths[0], len(calls), command)
	}
	if got, found := outputs[calls[0]]; !found || got != output {
		c.Failf("%s's output for %q = %q (found %t), want %q", paths[0], command, got, found, output)
	}
}
