//go:build journey

package journey

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// The project-tier template upstream changes, the file it scaffolds into the owner's project, and the
// line and commit subject the change adds.
const (
	movedTemplate = "project/rules/git-writes-through-gitter.md"
	movedLocal    = ".claude/rules/git-writes-through-gitter.md"
	movedLine     = "- A newer Professor adds this line for the adopter of the journey."
	movedSubject  = "rules: a line for the journey's adopter"
)

// upstreamMoves: a newer Professor reaches the owner's clone, as a pull would land it: one commit that
// changes one project-tier template. It is the fixture's one write into the clone; its cleanup moves
// the clone's HEAD back and restores the template, leaving every other file of the clone as it is.
func upstreamMoves(c *harness.Ctx, s State) State {
	clone := professorClone(s)
	before := strings.TrimSpace(read(c, "git", "-C", clone, "rev-parse", "HEAD"))
	c.Cleanup(func() { read(c, "git", "-C", clone, "reset", "-q", "--keep", before) })
	file := filepath.Join(clone, "templates", filepath.FromSlash(movedTemplate))
	raw, err := os.ReadFile(file)
	if err != nil {
		c.Failf("read %s: %v", file, err)
	}
	if err := os.WriteFile(file, append(raw, movedLine+"\n"...), 0o644); err != nil {
		c.Failf("write %s: %v", file, err)
	}
	read(c, "git", "-C", clone, "-c", "user.name=upstream", "-c", "user.email=upstream@journey.invalid",
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", movedSubject, "--", "templates/"+movedTemplate)
	if got := read(c, "git", "-C", clone, "status", "--porcelain", "--", "templates/"+movedTemplate); got != "" {
		c.Failf("the clone's template is not committed: %q", got)
	}
	return s
}
