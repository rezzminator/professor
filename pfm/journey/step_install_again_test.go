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
	expectInstallErrors(c)
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
		`printf '%s\n' "$2" > "$1.tmp" && mv "$1.tmp" "$1"`,
		"sh",
		stats,
		statsBody,
	); r.Code != 0 {
		c.Failf("replacing %s exited %d: %s%s", stats, r.Code, r.Stdout, r.Stderr)
	}

	term := c.Terminal("again")
	term.WaitFor(prompt)
	launcher := filepath.Join(s.Home, ".local", "bin", "claude")
	if got := read(c, "sh", "-c", "command -v claude"); got != launcher+"\n" ||
		!strings.Contains(read(c, "cat", launcher), "internal claude-launch") {
		c.Failf("claude on the owner's PATH is %q, want pfm's launcher %s, which the plugin step meets", got, launcher)
	}
	starts, definitions := serviceStarts(c), serviceDefinitions(c)
	renamed := renameRule(c, s, term)
	var second time.Time
	for i, label := range []string{"install-1", "install-2"} {
		if i == 1 {
			swapBinary(c, s)
			second = time.Now()
		}
		c.Poll("pfm's name-sync and schedule jobs at rest before "+label, 20*time.Second, func() (bool, string) {
			running := jobsRunning(c)
			return running == "", running
		})
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
			if i == 0 && !strings.Contains(change, "stats-cache.json") && !renamed.MatchString(change) {
				c.Failf("the first install changed %q beyond the stats-cache repair and the renamed rule (%s)",
					change, out)
			}
		}
		for pid, p := range processes(c) {
			if strings.Contains(p.Args, "claude-launch") {
				c.Failf("after %s process %d still runs pfm's claude launcher: %s", label, pid, p.Args)
			}
		}
		if n := strings.Count(text, "has live chats"); n > 1 {
			c.Failf("%s printed the live-chat deferral %d times, want at most once (%s)", label, n, out)
		}
	}
	dir := filepath.Join(s.Home, ".claude", "rules")
	link, target := filepath.Join(dir, "rm-rf-literal-path.md"),
		filepath.Join(professorClone(s), "templates", "global", "rules", "rm-rf-literal-path.md")
	if got, dead := read(
		c,
		"readlink",
		link,
	), read(
		c,
		"find",
		dir,
		"-type",
		"l",
		"!",
		"-exec",
		"test",
		"-e",
		"{}",
		";",
		"-print",
	); got != target+"\n" ||
		dead != "" {
		c.Failf("after the installs %s links to %q and %s holds dead links %q; want %s and none",
			link, got, dir, dead, target)
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
	if got := serviceStarts(c); got != starts {
		c.Failf("pfm's services were restarted by installs that changed none of them:\n%s\nwant as before:\n%s",
			got, starts)
	}
	if got := serviceDefinitions(c); got != definitions {
		c.Failf("pfm's service definitions after the installs and the binary swap:\n%s\nwant as before:\n%s",
			got, definitions)
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

// expectInstallErrors declares the ERROR records every pfm install --yes writes on the journey machine:
// no sudo for the managed settings, no network for the price list.
func expectInstallErrors(c *harness.Ctx) {
	c.Expect(`"step":"managed-cleanup /etc/claude-code/managed-settings.d/pfm.json not written"`)
	c.Expect(`"host":"platform.claude.com"`)
}

// renameRule: upstream renames a global rule in the clone, as a pull lands it (one commit), and the
// owner previews `pfm install` (no --yes: a dry run), which passes with no FAIL row although the old rule's
// link in ~/.claude/rules is dead. It returns the pattern of the install rows the rename may cause: the
// two links and the global rules each engine reads.
func renameRule(c *harness.Ctx, s State, term *harness.Terminal) *regexp.Regexp {
	from, to := "rm-rf-absolute-path.md", "rm-rf-literal-path.md"
	if r := c.Read("sh", "-c", `cd "$1" && git mv "templates/global/rules/$2" "templates/global/rules/$3" &&
git -c user.name=upstream -c user.email=upstream@example.invalid commit -q -m "rename $2 to $3"`,
		"sh", professorClone(s), from, to); r.Code != 0 {
		c.Failf("renaming the rule %s to %s in the clone exited %d: %s%s", from, to, r.Code, r.Stdout, r.Stderr)
	}
	out, errs, code := typedRun(c, term, "install-dry", "(cd ~/.professor && pfm install)")
	_, failed, summary := installPlan(out)
	if code != 0 || len(failed) > 0 || summary == "" {
		c.Failf("pfm install (a dry run) after the rule's rename exited %d, FAIL rows %q, summary %q; stderr %q",
			code, failed, summary, errs)
	}
	return regexp.MustCompile(regexp.QuoteMeta(from) + "|" + regexp.QuoteMeta(to) +
		`|^write \S+/\.codex/AGENTS\.md \(global rules block\)$|^write \S+/global-always-on\.md \(global rules for OpenCode\)$`)
}

// swapBinary replaces the installed pfm with a byte-identical copy at a new inode, as a rebuild's
// install does (a copy beside it, moved over it).
func swapBinary(c *harness.Ctx, s State) {
	bin := filepath.Join(s.Home, ".local", "bin", "pfm")
	if r := c.Read("sh", "-c", `cp -p "$1" "$1.swap" && mv -f "$1.swap" "$1"`, "sh", bin); r.Code != 0 {
		c.Failf("swapping %s for a copy exited %d: %s%s", bin, r.Code, r.Stdout, r.Stderr)
	}
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
