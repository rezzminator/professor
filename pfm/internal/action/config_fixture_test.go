package action

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func testMachineConfig(home string) pfmconfig.Config {
	machine := pfmconfig.Defaults(home, []string{
		pfmconfig.DefaultAccountDir(home, 1) + "/projects",
		pfmconfig.DefaultAccountDir(home, 2) + "/projects",
		pfmconfig.DefaultAccountDir(home, 3) + "/projects",
	})
	for _, account := range machine.Accounts {
		if err := os.MkdirAll(account.ConfigDir, 0o700); err != nil {
			panic(fmt.Sprintf("fixture account dir %s: %v", account.ConfigDir, err))
		}
	}
	machine.CodexAccounts = []pfmconfig.CodexAccount{
		{ID: 1, Home: home + "/.codex"},
		{ID: 2, Home: home + "/.codex-2"},
		{ID: 3, Home: home + "/.codex-3"},
	}
	for _, account := range machine.CodexAccounts {
		if err := os.MkdirAll(account.Home, 0o700); err != nil {
			panic(fmt.Sprintf("fixture Codex home %s: %v", account.Home, err))
		}
		if err := os.WriteFile(
			filepath.Join(account.Home, "auth.json"),
			[]byte(`{"tokens":{"access_token":"fixture","account_id":"fixture"}}`),
			0o600,
		); err != nil {
			panic(fmt.Sprintf("fixture Codex auth %s: %v", account.Home, err))
		}
	}
	return machine
}

func synthesizeWithTestConfig(request Request) (Plan, error) {
	if len(request.Config.Accounts) == 0 {
		request.Config = testMachineConfig(request.Home)
	}
	return Synthesize(request)
}

func headlessWithTestConfig(request HeadlessRequest) (HeadlessPlan, error) {
	if len(request.Config.Accounts) == 0 {
		request.Config = testMachineConfig(request.Home)
	}
	return HeadlessRun(request)
}

func openWithTestConfig(executor *Executor, ctx context.Context, request Request) (string, error) {
	if len(request.Config.Accounts) == 0 {
		request.Config = testMachineConfig(request.Home)
	}
	return executor.Open(ctx, request)
}

func TestMachineConfigCreatesAccountDirs(t *testing.T) {
	home := t.TempDir()
	machine := testMachineConfig(home)
	if len(machine.Accounts) != 3 {
		t.Fatalf("fixture roster has %d accounts, want 3", len(machine.Accounts))
	}
	for _, account := range machine.Accounts {
		dir := pfmconfig.DefaultAccountDir(home, account.ID)
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("fixture account dir %s: %v", dir, err)
		}
		if account.ConfigDir != dir || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("account %d dir = %q, mode = %v; want %s (0700)", account.ID, account.ConfigDir, info.Mode(), dir)
		}
	}
}

func TestMachineConfigAccountDirFailure(t *testing.T) {
	home := t.TempDir()
	dir := pfmconfig.DefaultAccountDir(home, 1)
	if err := os.WriteFile(filepath.Dir(dir), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := os.MkdirAll(dir, 0o700)
	if wantErr == nil {
		t.Fatal("blocked parent accepted as an account directory")
	}
	defer func() {
		if got, want := fmt.Sprint(recover()), fmt.Sprintf("fixture account dir %s: %v", dir, wantErr); got != want {
			t.Fatalf("fixture panic = %q, want %q", got, want)
		}
	}()
	testMachineConfig(home)
}
