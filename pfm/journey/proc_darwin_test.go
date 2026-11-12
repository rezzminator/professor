//go:build journey

package journey

import (
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// processCwd is process pid's working directory as lsof names it, "" when it is gone since the listing.
func processCwd(c *harness.Ctx, pid int) string {
	r := c.Read("lsof", "-a", "-d", "cwd", "-p", strconv.Itoa(pid), "-Fn")
	for _, line := range strings.Split(r.Stdout, "\n") {
		if name, ok := strings.CutPrefix(line, "n"); ok {
			return name
		}
	}
	return ""
}
