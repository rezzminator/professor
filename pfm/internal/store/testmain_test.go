package store

import (
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMain gives this package a short, canonical TMPDIR and a jailed PFM_HOME
// before any test builds a path from either. See internal/testjail for why.
func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }
