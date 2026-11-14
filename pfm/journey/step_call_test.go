//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// unsignedFromOwner is what a plain terminal's `pfm chat call --allow-unsigned` prints on stderr: it
// is no chat, so it derives no sender, and it says the recipient cannot verify it.
const unsignedFromOwner = "pfm: identify message sender: ERROR: this process is not inside a pfm chat: no live tmux " +
	"pane or Herdr pane holds it\nWARNING: sent UNSIGNED (--allow-unsigned) — this process derived no identity of " +
	"its own, so the recipient cannot verify who sent this and should not act on it. If this ran DETACHED " +
	"(setsid/nohup/disowned), that is why: detaching severs the process chain the handle is recovered from. Send " +
	"from the chat itself, or state it: CHAT_SENDER_SESSION=$(pfm whoami) CHAT_SENDER_LABEL=<label> <command>.\n"

// unsignedFooter is the footer pfm puts on an unsigned message, after two spaces.
const unsignedFooter = "  — UNSIGNED — sender identity underivable; no tmux session (ambient or via ancestry); no session id"

// call: from his plain terminal the owner calls the child with a message that makes it answer; pfm
// prints that it called the child's Herdr pane live, the reply paints in the child's tab, and the
// child's transcript grows by exactly the one unsigned user record and its answer, every earlier entry
// unchanged (escape chat-mesh/call-from-cli, delivered once to its actual pane). Then he leaves a draft
// in the child's composer and calls again: pfm refuses with exit 6, types nothing, and the draft stays
// in the composer, the transcript unchanged; he deletes the draft.
func call(c *harness.Ctx, s State) State {
	term, owner := c.Terminal(s.Term), c.Terminal(meshOwner)
	child := s.Mesh.ChildName
	term.Press(harness.Ctrl('b'), harness.Key("n"))
	term.WaitScreen("the child's tab", 20*time.Second, func(screen string) bool {
		return strings.Contains(screen, "until_gone") && !strings.Contains(screen, "spawn a child")
	})
	before := entries(c, child)
	pong := c.Unique("pong")
	message := directive(turnStep(pong))
	out, errs, code := typedRun(c, owner, "call", "pfm chat call --allow-unsigned "+child+" "+shellQuote(message))
	if delivered := herdrDelivered(
		s.Mesh.ChildPane,
	); code != 0 || !delivered.MatchString(strings.TrimSuffix(out, "\n")) ||
		!strings.HasSuffix(out, "\n") ||
		errs != unsignedFromOwner {
		c.Failf("pfm chat call %s: exit %d, stdout %q, stderr %q; want exit 0, stdout matching %s, stderr %q",
			child, code, out, errs, delivered, unsignedFromOwner)
	}
	c.Logf("pfm chat call %s: %s", child, strings.TrimSpace(out))
	term.WaitFor("⏺ "+pong, 20*time.Second)
	after := entries(c, child)
	grown := append(slices.Clone(before), entry{"user", message + unsignedFooter}, entry{"assistant", pong})
	if !slices.Equal(after, grown) {
		c.Failf("%s's transcript after one call = %+v, want %+v", child, after, grown)
	}

	// Herdr marks the turn over once its hook lands, later than the paint on a loaded machine; a call
	// meeting a working agent takes Herdr's raw path, whose refusal words it differently.
	c.Poll("Herdr status idle for "+s.Mesh.ChildPane, 20*time.Second, func() (bool, string) {
		got := herdrStatus(c, s, s.Mesh.ChildPane)
		return got == "idle", "status " + got
	})
	draft := c.Unique("draft")
	term.Type(draft)
	term.WaitFor("❯ " + draft)
	out, errs, code = typedRun(c, owner, "draft-call",
		"pfm chat call --allow-unsigned "+child+" "+shellQuote(directive(turnStep(c.Unique("over")))))
	refused := "pfm: identify message sender: ERROR: this process is not inside a pfm chat: no live tmux pane or " +
		"Herdr pane holds it\nABORT: native composer is not empty and ready; nothing was typed, existing draft/menu preserved\n"
	if code != 6 || out != "" || errs != refused {
		c.Failf("pfm chat call %s over a draft: exit %d, stdout %q, stderr %q; want exit 6, no stdout, stderr %q",
			child, code, out, errs, refused)
	}
	if line := composerLine(term.Screen()); line != "❯ "+draft {
		c.Failf("the child's composer after the refused call = %q, want the draft %q kept", line, "❯ "+draft)
	}
	if got := entries(c, child); !slices.Equal(got, after) {
		c.Failf("%s's transcript after the refused call = %+v, want it unchanged: %+v", child, got, after)
	}
	for range draft {
		term.Press(harness.Backspace)
	}
	term.WaitScreen("the child's composer empty", stepBound, func(screen string) bool {
		return composerLine(screen) == "❯"
	})
	return s
}

// herdrDelivered matches pfm's receipt of a call it typed live into a Herdr pane. pfm then proves the
// started turn from Herdr's agent state within its proof budget (500 ms); on a loaded machine the hook
// that publishes it lands later or Herdr's socket answers late, and pfm says why it proved no turn
// while still reporting the delivery the transcript then shows.
func herdrDelivered(pane string) *regexp.Regexp {
	return regexp.MustCompile(`^called LIVE into ` + regexp.QuoteMeta(fmt.Sprintf("%q", pane)) +
		` — Herdr agent_prompted — (Herdr turn confirmed|acknowledged input but proved no started turn: .+)$`)
}

// entry is one transcript entry as `pfm chat read --json` prints it, less its time.
type entry struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// entries is a chat's whole transcript through `pfm chat read --json`, never truncated.
func entries(c *harness.Ctx, name string) []entry {
	out := read(c, "pfm", "chat", "read", name, "--tail", "1000000", "--json")
	var read struct {
		Entries   []entry `json:"entries"`
		Truncated bool    `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(out), &read); err != nil || read.Truncated {
		c.Failf("pfm chat read %s --json: truncated %v, %v:\n%s", name, read.Truncated, err, out)
	}
	return read.Entries
}

// typedRun types command at term's prompt with its output sent to files under the owner's home, and
// returns its stdout, stderr and exit code.
func typedRun(c *harness.Ctx, term *harness.Terminal, label, command string) (stdout, stderr string, code int) {
	base := ownerHome + "/" + c.Unique(label)
	code = shellRun(term, label, "{ "+command+"; } >"+base+".out 2>"+base+".err", stepBound)
	stdout, stderr = read(c, "cat", base+".out"), read(c, "cat", base+".err")
	if r := c.Read("rm", base+".out", base+".err"); r.Code != 0 {
		c.Logf("rm %s.{out,err} exited %d: %s", base, r.Code, r.Stderr)
	}
	return stdout, stderr, code
}

// directive is a prompt the fake engine plays as these steps, in one turn.
func directive(steps ...map[string]any) string {
	encoded, err := json.Marshal(steps)
	if err != nil {
		panic(fmt.Sprintf("encode fake-engine steps %v: %v", steps, err))
	}
	return "mock-engine: " + string(encoded)
}

// turnStep is a fake-engine turn that answers reply.
func turnStep(reply string) map[string]any { return map[string]any{"type": "turn", "reply": reply} }

// mcpStep is a fake-engine call of pfm's MCP tool with input.
func mcpStep(tool string, input map[string]any) map[string]any {
	return map[string]any{"type": "mcp", "tool": tool, "input": input}
}

// shellQuote is text as one single-quoted shell word.
func shellQuote(text string) string { return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'" }

// composerLine is the fake Claude's composer line on a Herdr screen: the pane's text after `❯`.
func composerLine(screen string) string {
	for _, line := range strings.Split(screen, "\n") {
		if text := strings.TrimSpace(paneText(line)); strings.HasPrefix(text, "❯") {
			return text
		}
	}
	return ""
}
