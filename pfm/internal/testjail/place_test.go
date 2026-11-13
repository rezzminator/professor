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
