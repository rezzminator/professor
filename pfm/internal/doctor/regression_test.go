package doctor

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/gather"
)

func TestHarnessCaptureUsesInjectedListener(t *testing.T) {
	listenErr := errors.New("fixture listener refused")
	_, err := captureHarnessPromptWithDeps(
		context.Background(),
		t.TempDir(),
		config.Config{},
		"",
		"",
		Dependencies{Listen: func(string, string) (net.Listener, error) { return nil, listenErr }},
	)
	if err == nil || !strings.Contains(err.Error(), listenErr.Error()) {
		t.Fatalf("captureHarnessPromptWithDeps() error = %v, want injected listener error", err)
	}
}

func TestTmuxTitlesDoctorReportsStringDriftAsDivergent(t *testing.T) {
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

func TestDoctorConfigRenderingShowsEffectiveValuesAndSources(t *testing.T) {
	root := jailTest(t)
	home := filepath.Join(root, "config-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "doctor-machine.json")
	if err := os.WriteFile(
		path,
		[]byte(
			`{"version":1,"accounts":[{"id":7,"configDir":"~/account-seven"}],"claude":{"permissionMode":"prompt","binary":"claude-fixture"},"codex":{"yolo":false,"binary":"codex-fixture"},"mcp":{"servers":{"chat":{"enabled":true}}}}`,
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	PrintConfig(&stdout, config.Runtime{Config: loaded})
	for _, want := range []string{"config version=2 effective (input=1 file)", "config accounts=7:" + filepath.Join(home, "account-seven"), "mcp.servers.chat.enabled=true (file)"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("PrintConfig() = %q, missing %q", stdout.String(), want)
		}
	}
}
