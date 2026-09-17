package installer

import (
	"os"
	"testing"

	"hostops/pfm/internal/testjail"
)

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }
