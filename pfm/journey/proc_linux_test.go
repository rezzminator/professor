//go:build journey

package journey

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// processCwd is process pid's working directory, "" when it is gone since the listing.
func processCwd(c *harness.Ctx, pid int) string {
	cwd, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	if err != nil {
		c.Failf("read process %d's working directory: %v", pid, err)
	}
	return strings.TrimSuffix(cwd, " (deleted)")
}
