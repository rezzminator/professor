package control

import (
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMain gives this package a short, canonical TMPDIR and a jailed home before any test builds a path
// from them (internal/testjail).
func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }
