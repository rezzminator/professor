package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestThemeStatusInAFreshHomeIsOff: `pfm theme` routes to the theme verb, which
// in a home that never ran `pfm theme on` says it is off and exits 0 without
// touching any surface.
func TestThemeStatusInAFreshHomeIsOff(t *testing.T) {
	jailTest(t)
	for _, args := range [][]string{{"theme"}, {"theme", "status"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("run(%q) code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
		if want := "pfm theme: off — pfm theme on turns it on\n"; stdout.String() != want {
			t.Fatalf("run(%q) stdout = %q, want %q", args, stdout.String(), want)
		}
	}
}

func TestRootHelpListsTheTheme(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "  theme ") {
		t.Fatalf("root help missing the theme line:\n%s", stdout.String())
	}
}
