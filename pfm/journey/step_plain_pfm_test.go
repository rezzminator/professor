//go:build journey

package journey

import (
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// plainPfm: in a plain terminal, in a project of its own, the owner opens the picker and closes it;
// no chat comes of it.
func plainPfm(c *harness.Ctx, s State) State {
	project := c.Unique("plain")
	dir := filepath.Join(s.Home, "work", project)
	term := c.Terminal("plain-pfm")
	term.WaitFor(prompt)
	term.Type("mkdir -p ~/work/" + project + " && cd ~/work/" + project + " && pfm\r")
	term.WaitFor("New chat → " + project)
	term.Press(harness.Esc)
	term.WaitScreen("the prompt back under the closed picker", stepBound, func(screen string) bool {
		lines := strings.Split(screen, "\n")
		return strings.TrimSpace(lines[len(lines)-1]) == prompt
	})
	for _, line := range strings.Split(read(c, "pfm", "ls", "--tsv"), "\n") {
		if f := strings.Split(line, "\t"); len(f) > 3 && strings.HasPrefix(f[0], "live-") && f[3] == dir {
			c.Failf("closing the picker left a live chat in %s: %s", dir, line)
		}
	}
	return s
}
