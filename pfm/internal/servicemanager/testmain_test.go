package servicemanager_test

import (
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMain gives this package a short, canonical TMPDIR and a jailed home
// before any test builds a path from them. See internal/testjail for why both
// properties matter.
func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }
