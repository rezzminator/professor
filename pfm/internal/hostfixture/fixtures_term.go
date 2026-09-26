package hostfixture

import "testing"

// BareTerm jails a fleet with TERM unset and LANG/LC_ALL forced to the
// POSIX "C" locale — the state statusline, ui's cosmos glyphs and
// hookentry's compact-nudge renudge must fall back to plain ASCII for
// rather than emit control sequences or Unicode glyphs a bare terminal
// cannot render.
func BareTerm(t *testing.T) Base {
	t.Helper()
	base := newBase(t)
	unsetEnv(t, base, "TERM")
	setEnv(t, base, "LANG", "C")
	setEnv(t, base, "LC_ALL", "C")
	return base
}
