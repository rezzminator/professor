package reload

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
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
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
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
		action.Quote(customBinary),
	} {
		if !strings.Contains(tmux.respawn, want) {
			t.Fatalf("respawn command %q lacks configured policy %q", tmux.respawn, want)
		}
	}
	if got := respawnEnv(t, tmux.respawn)["CACHE_LIVE_CONTROL_MAIN_TTL"]; got != "1h" {
		t.Fatalf("reload cache setting = %q", got)
	}
	if strings.Contains(tmux.respawn, "skip-permissions") {
		t.Fatalf("prompt permission policy still armed bypass flags: %q", tmux.respawn)
	}
}

func TestReloadMachineAccountDirs(t *testing.T) {
	machine := reloadTestMachine("", t.TempDir())
	for _, account := range machine.Accounts {
		info, err := os.Stat(account.ConfigDir)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("account dir %s: mode %v", account.ConfigDir, info.Mode())
		}
	}
}

func TestReloadMachineAccountDirFailure(t *testing.T) {
	for _, id := range []int{1, 2} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			home := t.TempDir()
			dir := pfmconfig.DefaultAccountDir(home, id)
			if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dir, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			wantErr := os.MkdirAll(dir, 0o700)
			defer func() {
				if got, want := fmt.Sprint(
					recover(),
				), fmt.Sprintf(
					"fixture account dir %s: %v",
					dir,
					wantErr,
				); got != want {
					t.Fatalf("fixture panic = %q, want %q", got, want)
				}
			}()
			reloadTestMachine("", home)
		})
	}
}
