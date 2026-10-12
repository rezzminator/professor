package codexprompt

import (
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMain runs this package under the test jail.
func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }
