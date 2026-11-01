package herdr

import (
	"errors"
	"testing"
)

// HostHasHerdr is the one Herdr-presence rule the probe and the hook reporter
// share: HERDR_BIN_PATH or herdr on PATH proves a Herdr, a nil seam proves
// nothing.
func TestHostHasHerdrIsTheOnePresenceRule(t *testing.T) {
	binSet := func(name string) string {
		if name == EnvBin {
			return "/opt/herdr/bin/herdr"
		}
		return ""
	}
	empty := func(string) string { return "" }
	found := func(string) (string, error) { return "/usr/bin/herdr", nil }
	missing := func(string) (string, error) { return "", errors.New("not on PATH") }
	for _, tc := range []struct {
		name     string
		getenv   func(string) string
		lookPath func(string) (string, error)
		want     bool
	}{
		{name: "HERDR_BIN_PATH", getenv: binSet, lookPath: missing, want: true},
		{name: "on PATH", getenv: empty, lookPath: found, want: true},
		{name: "neither", getenv: empty, lookPath: missing},
		{name: "nil seams", getenv: nil, lookPath: nil},
		{name: "nil getenv still finds PATH", getenv: nil, lookPath: found, want: true},
	} {
		if got := HostHasHerdr(tc.getenv, tc.lookPath); got != tc.want {
			t.Errorf("%s: HostHasHerdr = %v, want %v", tc.name, got, tc.want)
		}
	}
}
