package reload

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/action"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
)

func TestRosterContainsFailsClosedWhenRosterIsEmpty(t *testing.T) {
	t.Parallel()
	if rosterContains(nil, 1) {
		t.Fatal("rosterContains(nil, 1) accepted an account invented outside config")
	}
}

func TestRunRespawnsWithConfiguredClaudePolicy(t *testing.T) {
	t.Parallel()
	tmux := &fakeReloadTmux{}
	configDir := filepath.Join(t.TempDir(), "account 42")
	customBinary := "/opt/tools/claude enterprise"
	machine := pfmconfig.Config{
		Claude:   pfmconfig.ClaudePrefs{PermissionMode: pfmconfig.PermissionPrompt, Binary: customBinary},
		Accounts: []pfmconfig.Account{{ID: 42, ConfigDir: configDir}},
		Sources:  map[string]pfmconfig.Source{"claude.binary": pfmconfig.SourceFile},
	}
	_, err := Run(
		context.Background(),
		Request{
			Engine:     pfmengine.Claude,
			SocketPath: "/tmp/tmux-1000/configured-reload",
			Pane:       "%7",
			SessionID:  "11111111-1111-4111-8111-111111111111",
			CWD:        "/jail/project",
			Account:    42,
			AccountIDs: []int{42},
			Cache1H:    true,
			Machine:    machine,
		},
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2},
		tmux,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, want := range []string{
		"CLAUDE_CONFIG_DIR=" + action.Quote(configDir),
		"ENABLE_PROMPT_CACHING_1H=1",
		action.Quote(customBinary),
	} {
		if !strings.Contains(tmux.respawn, want) {
			t.Fatalf("respawn command %q lacks configured policy %q", tmux.respawn, want)
		}
	}
	if strings.Contains(tmux.respawn, "skip-permissions") {
		t.Fatalf("prompt permission policy still armed bypass flags: %q", tmux.respawn)
	}
}
