//go:build journey

package journey

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// reinstall: with the branch's Herdr session open and its tab painter running, a rebuild replaces
// ~/.local/bin/pfm (a copy at a new inode, swapBinary) and the owner types pfm install --yes. The
// install names the painter's refresh as a change its summary counts, and the painters that ran the
// replaced binary are gone, a new one painting in their place from /, never the directory the install
// ran in.
func reinstall(c *harness.Ctx, s State) State {
	before := painters(c)
	if len(before) == 0 {
		c.Failf("no Herdr tab painter (pfm internal herdr-indicators) runs before the reinstall")
	}
	expectInstallErrors(c)
	term := c.Terminal("reinstall")
	term.WaitFor(prompt)
	swapBinary(c, s)
	out := filepath.Join(s.Home, "reinstall.out")
	code := shellRun(term, "reinstall", "clear && (cd ~/.professor && pfm install --yes) > ~/reinstall.out 2>&1",
		150*time.Second)
	text := read(c, "cat", out)
	changes, failed, summary := installPlan(text)
	refreshed := slices.ContainsFunc(changes, painterRefresh.MatchString)
	if code != 0 || len(failed) > 0 || !refreshed ||
		summary != "" && !strings.HasPrefix(summary, "summary changed="+strconv.Itoa(len(changes))+" ") {
		c.Failf("pfm install --yes after the binary swap exited %d, FAIL rows %q, changes %q, summary %q; "+
			"want exit 0, no FAIL row, the painter's refresh among the changes and counted (%s)",
			code, failed, changes, summary, out)
	}
	gone := "the painters of the replaced binary gone and a new one painting from /"
	c.Poll(gone, 10*time.Second, func() (bool, string) {
		after := painters(c)
		for pid := range after {
			if before[pid] {
				return false, fmt.Sprintf("painter %d of the replaced binary still runs (painters %v)", pid, after)
			}
			if cwd := processCwd(c, pid); cwd != "/" {
				return false, fmt.Sprintf("painter %d works in %q, not / (it outlives the directory it started in)",
					pid, cwd)
			}
		}
		return len(after) > 0, fmt.Sprintf("painters %v", after)
	})
	return s
}

// painterRefresh is the install row that replaces a Herdr tab painter running a replaced binary.
var painterRefresh = regexp.MustCompile(
	`^replaced [1-9]\d* stale Herdr indicator painter\(s\) on [1-9]\d* socket\(s\)$`)

// painters is the pid set of every Herdr tab painter (`pfm internal herdr-indicators`) on the machine.
func painters(c *harness.Ctx) map[int]bool {
	set := map[int]bool{}
	for pid, p := range processes(c) {
		if painterProcess(p) {
			set[pid] = true
		}
	}
	return set
}

// painterProcess reports whether p is a Herdr tab painter: pfm's `internal herdr-indicators`.
func painterProcess(p process) bool {
	_, args, _ := strings.Cut(p.Args, " ")
	return pfmProcess(p) &&
		(args == "internal herdr-indicators" || strings.HasPrefix(args, "internal herdr-indicators "))
}
