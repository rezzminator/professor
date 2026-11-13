package professor

import (
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testjail/fakegit"
)

// storeGit is the git every store test builds and reads its fixtures with.
var storeGit = &fakegit.Runner{}

func TestMain(m *testing.M) {
	StubStoreRunnerForTest(storeGit)
	os.Exit(testjail.Run(m))
}
