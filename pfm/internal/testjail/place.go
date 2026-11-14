package testjail

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Place roots an invented absolute fixture path (/work/app, /jail/home, /fixture/herdr.sock) in the
// package's jail directory, where Run points PFM_HOME: the test names the place the way its
// assertions read, and code that stats it, walks up from it or dials it reaches the jail, never the
// host's own /work. It panics before Run has set the jail (a package-level initializer): a failure
// never reads as a path on the host.
func Place(path string) string {
	home := paths.EnvOr(paths.EnvHome, "")
	if home == "" {
		panic(fmt.Sprintf("testjail.Place(%q) before testjail.Run set %s", path, paths.EnvHome))
	}
	placed := filepath.Join(home, "place", path)
	if strings.HasSuffix(path, "/") { // a prefix a test appends a name to
		placed += "/"
	}
	return placed
}

// TempPath matches a path under the temp root Run set (TMPDIR, canonical: macOS's /tmp reads
// /private/tmp), followed by tail, the rest of the path a test's output normaliser writes as one
// token. A pattern on the literal /tmp/ would leave macOS's /private in front of the token.
func TempPath(tail string) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(paths.TempRoot()) + tail)
}
