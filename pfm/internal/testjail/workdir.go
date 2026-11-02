package testjail

import (
	"os"
	"strings"
	"testing"
)

// ChdirUnnamable moves the test into a directory os.Getwd cannot name on any
// platform: its path passes every PATH_MAX (4096 on Linux, 1024 on macOS), so
// getcwd(3) refuses it, and it sits deeper than the 341 levels os.Getwd's own
// ".." walk climbs before it gives up with "getwd: file name too long". A
// removed directory is no such fixture: macOS getcwd still names it.
func ChdirUnnamable(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("PWD", "") // os.Getwd returns a $PWD that names "." unasked
	name := strings.Repeat("d", 12)
	for range 345 {
		if err := os.Mkdir(name, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(name); err != nil {
			t.Fatal(err)
		}
	}
}
