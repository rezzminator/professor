//go:build journey

package journey

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// installRow is one row of pfm install's plan: its verdict word, then its text.
var installRow = regexp.MustCompile(`^\s+(change|warn|skip|fail|FAIL|block|BLOCK)\s+(.*)$`)

// installAgain: with a chat of its own live (claude typed in a plain terminal) and its account's
// stats-cache.json replaced by a plain file, as Claude's /stats does, the owner runs pfm install
// --yes twice. Each finishes with no FAIL row; the first changes nothing but the repair, the second
// nothing at all and refreshes no marketplace; the account's stats file is kept, and the live chat
// still answers a turn.
func installAgain(c *harness.Ctx, s State) State {
	c.Expect(`"step":"managed-cleanup /etc/claude-code/managed-settings.d/pfm.json not written"`)
	c.Expect(`"host":"platform.claude.com"`)
	chat := State{Home: s.Home, Term: "again-chat"}
	chatTerm := c.Terminal(chat.Term)
	chatTerm.WaitFor(prompt)
	c.Cleanup(func() { stopPlainChat(c, &chat, chatTerm) })
	chatTerm.Type("cd $HOME/work/demo && claude\r")
	chatTerm.WaitFor("❯", 20*time.Second)
	chat = plainClaudeLive(c, chat, attached(c, chatTerm))

	stats := filepath.Join(s.Home, ".cc", "1", "stats-cache.json")
	statsBody := `{"version":1,"owner":"` + c.Unique("stats") + `"}`
	if r := c.Read(
		"sh",
		"-c",
		`rm -f "$1" && printf '%s\n' "$2" > "$1.tmp" && mv "$1.tmp" "$1"`,
		"sh",
		stats,
		statsBody,
	); r.Code != 0 {
		c.Failf("replacing %s exited %d: %s%s", stats, r.Code, r.Stdout, r.Stderr)
	}

	term := c.Terminal("again")
	term.WaitFor(prompt)
	var second time.Time
	for i, label := range []string{"install-1", "install-2"} {
		if i == 1 {
			second = time.Now()
		}
		out := filepath.Join(s.Home, label+".out")
		code := shellRun(
			term,
			label,
			"clear && (cd ~/.professor && pfm install --yes) > ~/"+label+".out 2>&1",
			150*time.Second,
		)
		text := read(c, "cat", out)
		if code != 0 {
			c.Failf("%s exited %d; its output (%s):\n%s", label, code, out, text)
		}
		changes, failed, summary := installPlan(text)
		if len(failed) > 0 {
			c.Failf("%s printed FAIL rows (%s):\n%s", label, out, strings.Join(failed, "\n"))
		}
		if summary == "" {
			c.Failf("%s printed no summary line (%s):\n%s", label, out, text)
		}
		c.Logf("%s: %s; changes %q", label, summary, changes)
		if i == 1 && (len(changes) != 0 || !strings.HasPrefix(summary, "summary changed=0 ")) {
			c.Failf("the second install changed %q (%s), want nothing (%s)", changes, summary, out)
		}
		for _, change := range changes {
			if i == 0 && !strings.Contains(change, "stats-cache.json") {
				c.Failf("the first install changed %q beyond the stats-cache repair (%s)", change, out)
			}
		}
		if n := strings.Count(text, "has live chats"); n > 1 {
			c.Failf("%s printed the live-chat deferral %d times, want at most once (%s)", label, n, out)
		}
	}
	var refreshes, installRecords []string
	for _, record := range activity(c, second, time.Time{}) {
		if strings.Contains(record, `"cmd":"install"`) {
			installRecords = append(installRecords, record)
		}
		if strings.Contains(record, "marketplace update") {
			refreshes = append(refreshes, record)
		}
	}
	if len(installRecords) == 0 || len(refreshes) != 0 {
		c.Failf(
			"activity log during the second install: %d install records, marketplace refreshes %q; want some and none",
			len(installRecords),
			refreshes,
		)
	}
	if got := read(c, "cat", stats); got != statsBody+"\n" {
		c.Failf("%s after the installs = %q, want the account's own %q", stats, got, statsBody+"\n")
	}

	reply := c.Unique("reply")
	chatTerm.Type(`after the installs mock-engine: {"type":"turn","reply":"` + reply + `"}` + "\r")
	chatTerm.WaitFor("⏺ "+reply, 20*time.Second)
	if live := plainClaudeLive(c, chat, attached(c, chatTerm)); live.Chat.Engine != chat.Chat.Engine {
		c.Failf("the chat's engine changed from %d to %d across the installs", chat.Chat.Engine, live.Chat.Engine)
	}
	return s
}

// installPlan reads pfm install's output: its change rows, its fail rows, its summary line.
func installPlan(text string) (changes, failed []string, summary string) {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "summary changed=") {
			summary = strings.TrimSpace(line)
			continue
		}
		m := installRow.FindStringSubmatch(line)
		switch {
		case m == nil:
		case m[1] == "change":
			changes = append(changes, m[2])
		case strings.EqualFold(m[1], "fail") || strings.EqualFold(m[1], "block"):
			failed = append(failed, fmt.Sprintf("%s %s", m[1], m[2]))
		}
	}
	return changes, failed, summary
}
