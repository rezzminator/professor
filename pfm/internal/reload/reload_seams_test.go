package reload

import (
	"context"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/hostfixture"
)

// TestRunRefusesWithNoTmuxClientOnAHostWithoutTmux pins hostfixture case 4
// (NoTmux) for reload: on a host whose tmux is missing (the fixture scripts
// FakeRunner's tmux lookup to ENOENT — the same shape doctor and spawn read
// as "tmux not installed"), Run is handed no tmux client and must refuse by
// name instead of panicking on a nil interface.
// Stays serial: hostfixture.NoTmux jails through t.Setenv, which forbids a
// parallel test in the same body.
func TestRunRefusesWithNoTmuxClientOnAHostWithoutTmux(t *testing.T) {
	base := hostfixture.NoTmux(t)
	_, err := Run(
		context.Background(),
		Request{
			Engine:     pfmengine.Claude,
			SocketPath: "/tmp/tmux-1000/probe-1",
			Pane:       "%1",
			Account:    1,
			AccountIDs: []int{1},
		},
		Options{SIDDir: base.Values.SIDDir, Delay: -1, Poll: -1, Clock: clock.Real},
		nil,
		nil,
		nil,
	)
	if err == nil || err.Error() != "reload requires a tmux client" {
		t.Fatalf("Run() on a no-tmux host error = %v, want the named tmux-client refusal", err)
	}
}
