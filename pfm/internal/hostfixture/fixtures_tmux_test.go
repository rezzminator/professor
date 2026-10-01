package hostfixture

import (
	"errors"
	"os/exec"
	"testing"
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
