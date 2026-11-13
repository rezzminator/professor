//go:build jail

package doctor

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestTmuxTitlesDoctorReportsStringDriftAsDivergent(t *testing.T) {
	testjail.TmuxServer(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root, socket := t.TempDir(), "titles-doctor"
	environment := append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
	start := exec.Command("tmux", "-L", socket, "-f", "/dev/null", "new-session", "-d", "-s", "fixture", "sleep", "120")
	start.Env = environment
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start tmux fixture: %v: %s", err, output)
	}
	t.Cleanup(func() {
		command := exec.Command("tmux", "-L", socket, "kill-server")
		command.Env = environment
		_ = command.Run()
	})
	for option, value := range map[string]string{"set-titles": "on", "set-titles-string": "custom-host-title"} {
		command := exec.Command("tmux", "-L", socket, "set-option", "-g", option, value)
		command.Env = environment
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", option, err, output)
		}
	}
	state, detail := readTmuxTitlesState(
		context.Background(),
		gather.TmuxProbe{Binary: "tmux", TmuxTmpDir: root},
		socket,
		true,
	)
	if state != titlesDivergent || !strings.Contains(detail, "custom-host-title") ||
		!strings.Contains(detail, config.TmuxTitlesString) {
		t.Fatalf("state=%q detail=%q, want divergent with actual and expected strings", state, detail)
	}
}
