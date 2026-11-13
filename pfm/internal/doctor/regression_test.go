package doctor

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
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
