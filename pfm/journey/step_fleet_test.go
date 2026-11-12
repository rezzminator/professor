//go:build journey

package journey

import (
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// fleet: after the day, every branch done, the owner starts a named chat in a scratch project, ends
// it and deletes the project's directory; pfm ls still carries the ended chat under its id, name and
// project, once, as every row is listed once; the picker draws its name; pfm chat ls --all prints its
// header; underneath, the whole machine is consistent (fleetWhole).
func fleet(c *harness.Ctx, s State) State {
	project := c.Unique("gone")
	reply := c.Unique("reply")
	term := c.Terminal("plain")
	term.WaitFor(prompt)
	if code := shellRun(
		term,
		"new",
		"mkdir -p ~/work/"+project+" && cd ~/work/"+project+" && pfm chat new --name "+project+
			` 'a turn mock-engine: {"type":"turn","reply":"`+reply+`"}'`,
		30*time.Second,
	); code != 0 {
		c.Failf("pfm chat new exited %d:\n%s", code, term.Screen())
	}
	c.Poll("chat "+project+" answered", 20*time.Second, func() (bool, string) {
		r := c.Read("pfm", "chat", "last", project)
		return r.Code == 0 && r.Stdout == reply+"\n", r.Stdout + r.Stderr
	})
	if code := shellRun(term, "end", "clear && pfm chat end "+project, 30*time.Second); code != 0 {
		c.Failf("pfm chat end %s exited %d:\n%s", project, code, term.Screen())
	}
	var before []string
	c.Poll("pfm ls carries the ended chat "+project, 20*time.Second, func() (bool, string) {
		table := read(c, "pfm", "ls", "--tsv")
		before = carried(table, project)
		return len(before) == 11 && before[0] != "live-claude", table
	})
	if code := shellRun(term, "rm", "cd ~ && rm -rf ~/work/"+project, 10*time.Second); code != 0 {
		c.Failf("deleting ~/work/%s exited %d:\n%s", project, code, term.Screen())
	}
	table := read(c, "pfm", "ls", "--tsv")
	after := carried(table, project)
	if after == nil || strings.Join(after[:5], "\t") != strings.Join(before[:5], "\t") {
		c.Failf("pfm ls row of %s after its directory went = %q, want kind, id, project, cwd and name of %q:\n%s",
			project, after, before, table)
	}
	seen := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(table), "\n")[1:] {
		if id := strings.Split(line, "\t")[1]; id != "" { // a launch row has no id
			seen[id]++
		}
	}
	for id, n := range seen {
		if n != 1 {
			c.Failf("pfm ls lists %s %d times, want once:\n%s", id, n, table)
		}
	}
	term.Type("clear && pfm\r")
	term.WaitScreen("the picker's first refresh drew "+project, 20*time.Second, func(screen string) bool {
		return strings.Contains(screen, "↻ "+project) && strings.Contains(screen, "find › type to find") &&
			!strings.Contains(screen, "⟳ refreshing")
	})
	term.Press(harness.Esc)
	term.WaitFor(prompt)
	if code := shellRun(term, "chats", "clear && pfm chat ls --all", 20*time.Second); code != 0 {
		c.Failf("pfm chat ls --all exited %d:\n%s", code, term.Screen())
	}
	term.WaitFor("live chats everywhere (name · session · state · herdr · dir · last activity):")
	fleetWhole(c)
	return s
}

// carried is the pfm ls --tsv row whose name is name, split into its fields; nil when none is.
func carried(table, name string) []string {
	for _, line := range strings.Split(table, "\n") {
		if fields := strings.Split(line, "\t"); len(fields) == 11 && fields[4] == name {
			return fields
		}
	}
	return nil
}
