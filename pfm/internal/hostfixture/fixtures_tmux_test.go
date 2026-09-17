package hostfixture

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"hostops/pfm/internal/deps"
)

func TestNoTmuxAnswersENOENTFromLookPath(t *testing.T) {
	base := NoTmux(t)
	_, err := base.Runner.LookPath("tmux")
	if err == nil {
		t.Fatal("LookPath(tmux) succeeded after NoTmux, want ENOENT")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("LookPath(tmux) error = %v, want exec.ErrNotFound (ENOENT)", err)
	}
}

// TestOldTmuxVersionsBelowTheRegisteredMinimum runs the SAME parse and
// comparison deps.Probe itself uses (deps.Registry's "tmux" entry, its
// Parse func, deps.AtLeast) against the fixture's scripted output, proving
// the fixture's claim against the real registry rather than a hand-picked
// re-implementation of "is this version old".
func TestOldTmuxVersionsBelowTheRegisteredMinimum(t *testing.T) {
	base := OldTmux(t)

	path, err := base.Runner.LookPath("tmux")
	if err != nil {
		t.Fatalf("LookPath(tmux): %v", err)
	}
	result, err := base.Runner.Run(context.Background(), []string{path, "-V"}, deps.RunOptions{})
	if err != nil {
		t.Fatalf("Run(tmux -V): %v", err)
	}

	var entry deps.Entry
	found := false
	for _, candidate := range deps.Registry() {
		if candidate.Name == "tmux" {
			entry, found = candidate, true
			break
		}
	}
	if !found {
		t.Fatal("deps.Registry() has no \"tmux\" entry")
	}
	version, err := entry.Parse(string(result.Stdout))
	if err != nil {
		t.Fatalf("entry.Parse(%q): %v", result.Stdout, err)
	}
	if deps.AtLeast(version, entry.MinVersion) {
		t.Fatalf("OldTmux's scripted version %q satisfies the minimum %q, want it below", version, entry.MinVersion)
	}
}
