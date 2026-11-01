package cmdparse_test

import (
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMain jails this package's tests. It lives in the external test package:
// testjail imports config, which imports this package for Quote, and an
// internal test file importing testjail back is a cycle Go rejects.
func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }
