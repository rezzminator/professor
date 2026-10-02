package hostfixture

import (
	"os/exec"
	"testing"
)

// NoTmux jails a fleet whose FakeRunner answers ENOENT for tmux — the state
// tmux, doctor, spawn and reload's host-tmux probe must report as "tmux not
// installed", never crash into a bare exec lookup error.
func NoTmux(t *testing.T) Base {
	t.Helper()
	base := newBase(t)
	base.Runner.ScriptLookPath("tmux", "", &exec.Error{Name: "tmux", Err: exec.ErrNotFound})
	return base
}
