package action

import (
	"os"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMain gives this package a short, canonical TMPDIR before any test builds
// a path from it. See internal/testjail for why both properties matter. The
// launch watch polls at test speed: every fake chat settles on its second read.
func TestMain(m *testing.M) {
	launchPoll = time.Millisecond
	os.Exit(testjail.Run(m))
}
