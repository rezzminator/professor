package testjail

import (
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestPlaceRootsAnInventedPathInTheJailDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	for _, row := range []struct{ path, want string }{
		{"/work/app", filepath.Join(home, "place", "work", "app")},
		{"/jail", filepath.Join(home, "place", "jail")},
		{"/fixture/herdr.sock", filepath.Join(home, "place", "fixture", "herdr.sock")},
		{"/accounts/1/projects/", filepath.Join(home, "place", "accounts", "1", "projects") + "/"},
	} {
		if got := Place(row.path); got != row.want {
			t.Errorf("Place(%q) = %q, want %q", row.path, got, row.want)
		}
	}
}

func TestPlaceRefusesWithoutAJailDirectory(t *testing.T) {
	t.Setenv(paths.EnvHome, "")
	defer func() {
		want := `testjail.Place("/work") before testjail.Run set ` + paths.EnvHome
		if got := recover(); got != want {
			t.Errorf("Place panicked with %v, want %q", got, want)
		}
	}()
	Place("/work")
}

// The temp root is the one Run set, so macOS's canonical /private/tmp is matched whole and a path
// outside the root is left as it is.
func TestTempPathMatchesFromTheTempRootRunSet(t *testing.T) {
	for _, row := range []struct{ root, tail, in, want string }{
		{"/private/tmp", `/[^\s"':]*`, "open /private/tmp/TestRun1/001/marker: absent", "open {tmp}: absent"},
		{"/private/tmp", `/[^\s"':]*?/\d{3}\b`, "at /private/tmp/TestRun1/002/.config/x.json", "at {tmp}/.config/x.json"},
		{"/tmp/u42", `/[^\s"':]*`, "open /tmp/u42/TestRun1/001/marker", "open {tmp}"},
		{"/tmp/u42", `/[^\s"':]*`, "open /tmp/other/marker", "open /tmp/other/marker"},
		{"/a.b", `/[^\s"':]*`, "open /axb/c", "open /axb/c"},
	} {
		t.Setenv("TMPDIR", row.root)
		pattern := TempPath(row.tail)
		if got := pattern.ReplaceAllString(row.in, "{tmp}"); got != row.want {
			t.Errorf("TempPath(%q) under TMPDIR=%s over %q = %q, want %q (pattern %s)",
				row.tail, row.root, row.in, got, row.want, pattern)
		}
	}
}
