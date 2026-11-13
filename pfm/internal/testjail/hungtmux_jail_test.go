package testjail

import (
	"os/exec"
	"strings"
	"testing"
)

// TestHungTmuxAnswersOnlyKillServer: the fake is first on PATH, kill-server
// exits with the asked code and an unknown subcommand fails loudly.
func TestHungTmuxAnswersOnlyKillServer(t *testing.T) {
	binary := HungTmux(t, 0)
	found, err := exec.LookPath("tmux")
	if err != nil || found != binary {
		t.Fatalf("tmux on PATH = %q, %v; want the fake %q", found, err, binary)
	}
	if err := exec.Command(binary, "-S", "/nowhere", "kill-server").Run(); err != nil {
		t.Fatalf("kill-server = %v, want exit 0", err)
	}
	output, err := exec.Command(binary, "-S", "/nowhere", "display-message").CombinedOutput()
	if err == nil || !strings.Contains(string(output), "unexpected") {
		t.Fatalf("display-message = %v %q, want a loud failure", err, output)
	}
}
