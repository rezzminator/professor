package update

import (
	"fmt"
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/professor"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testjail/fakegit"
)

// updateGit is the git every update test's fixtures and update's own calls run
// through: update's runner and the professor store's both point at it.
var updateGit = &fakegit.Runner{}

func TestMain(m *testing.M) {
	StubRunnerForTest(updateGit)
	professor.StubStoreRunnerForTest(updateGit)
	code := testjail.Run(m)
	if err := removeUpdateFixtureTemplates(); err != nil {
		fmt.Fprintf(os.Stderr, "update tests: remove fixture templates: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
