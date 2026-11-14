package gathertest_test

import (
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMain jails this package's tests like every other package's: a short,
// canonical TMPDIR and no reach into the operator's home or fleet.
func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }
