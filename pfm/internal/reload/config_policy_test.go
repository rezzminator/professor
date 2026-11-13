package reload

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestRosterContainsFailsClosedWhenRosterIsEmpty(t *testing.T) {
	t.Parallel()
	if rosterContains(nil, 1) {
		t.Fatal("rosterContains(nil, 1) accepted an account invented outside config")
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
