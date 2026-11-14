package testjail

import (
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// startInJail makes Run start the package's tests in the jail directory instead of the package
// directory (RunInJail sets it).
var startInJail bool

// RunInJail is Run for a package whose tests start in the jail directory: code that reads a
// relative path or walks up from the working directory (a project-root or baseline walk) then
// reaches the jail, never the checkout. A package whose tests read their own testdata by a
// relative path calls Run; one that builds from the module asks ModuleRoot.
func RunInJail(m *testing.M) int {
	startInJail = true
	return Run(m)
}

// enterJailDir moves the process into the jail directory when RunInJail asked for it, reporting
// a failure as a setup failure; false when the move failed.
func enterJailDir() bool {
	if !startInJail {
		return true
	}
	if err := os.Chdir(paths.EnvOr(paths.EnvHome, "")); err != nil {
		warnSetup("start in the jail directory: %v", err)
		return false
	}
	return true
}
