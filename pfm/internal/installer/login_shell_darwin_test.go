package installer

import "testing"

func TestLookupLoginShellDarwin(t *testing.T) {
	checkDarwinLoginShellCases(t, lookupLoginShell)
}
