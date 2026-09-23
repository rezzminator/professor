package backfill

import (
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMain gives this package a short, canonical TMPDIR before any test builds
// a path from it. See internal/testjail for why both properties matter.
func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }
