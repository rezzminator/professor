package hostfixture

import (
	"os/exec"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
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

// OldTmux jails a fleet whose FakeRunner resolves tmux to a build below
// deps' registered minimum (1.8; deps.Registry's "tmux" entry) — the state
// deps.Probe and doctor must report as a version failure, not silently
// treat as healthy.
func OldTmux(t *testing.T) Base {
	t.Helper()
	base := newBase(t)
	base.Runner.ScriptLookPath("tmux", "/usr/bin/tmux", nil)
	base.Runner.Script(
		[]string{"/usr/bin/tmux", "-V"},
		deps.RunResult{Stdout: []byte("tmux 1.6\n")},
		nil,
	)
	return base
}
