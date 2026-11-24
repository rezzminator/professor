package loginshell

import "testing"

func TestLookupLoginShellDarwin(t *testing.T) {
	checkDarwinLoginShellCases(t, Lookup)
}
