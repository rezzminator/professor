package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestInstallCheckRefusesYesAndRollback(t *testing.T) {
	runtime := commandRuntime{Paths: paths.Values{Home: t.TempDir()}}
	for _, args := range [][]string{
		{"--check", "--yes"},
		{"--yes", "--check"},
		{"--check", "--rollback", "20260102T030405Z"},
		{"--rollback", "20260102T030405Z", "--check"},
	} {
		var stdout, stderr bytes.Buffer
		if code := runInstall(args, &stdout, &stderr, runtime); code != 2 {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q, want usage 2", args, code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "[--check]") {
			t.Fatalf("args=%v stderr=%q, want the usage line naming --check", args, stderr.String())
		}
	}
}

// installCheckTree maps every path under root to its content, so a test can
// prove `pfm install --check` wrote nothing.
func installCheckTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			tree[path] = "dir"
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		tree[path] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return tree
}

// installCheckHome stages a host with one Claude account under {home}/.cc/1
// and its config in the clone (migrated) or in ~/.config/pfm (legacy, the
// host a new binary refuses until pfm install moves it).
func installCheckHome(t *testing.T, legacy bool) (home, clone, account string) {
	t.Helper()
	home = t.TempDir()
	clone, account = filepath.Join(home, "clone"), filepath.Join(home, ".cc", "1")
	for _, dir := range []string{clone, account, filepath.Join(home, "proc")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for key, value := range map[string]string{
		"HOME": home, paths.EnvHome: home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		paths.EnvProcRoot: filepath.Join(home, "proc"), paths.EnvStateDB: "", paths.EnvCacheDB: "",
		paths.EnvConfig: "",
	} {
		t.Setenv(key, value)
	}
	if err := os.Unsetenv(paths.EnvConfig); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(clone, pfmconfig.FileName)
	if legacy {
		configPath = filepath.Join(home, ".config", "pfm", pfmconfig.FileName)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"version":2,"accounts":[{"id":1,"configDir":%q}]}`+"\n", account)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, clone, account
}

func TestInstallCheckAnswersWithTheApplyRefusalsAndWritesNothing(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	runInstaller = func(ctx context.Context, options installer.Options) (installer.Report, error) {
		if options.Mode != installer.ModeDryRun {
			t.Fatalf("--check ran the installer in mode %v, want only the space preflight's dry run", options.Mode)
		}
		return previous(ctx, options)
	}
	// check runs --check through the production loader (main.go's
	// LoadInstallRuntime for install) and proves HOME unchanged.
	check := func(t *testing.T, home, configFlag string) (code int, stdout, stderr string) {
		t.Helper()
		runtime, err := pfmconfig.LoadInstallRuntime(configFlag)
		if err != nil {
			t.Fatalf("LoadInstallRuntime: %v", err)
		}
		before := installCheckTree(t, home)
		var out, errOut bytes.Buffer
		code = runInstall([]string{"--check"}, &out, &errOut, runtime)
		if after := installCheckTree(t, home); !reflect.DeepEqual(before, after) {
			t.Fatalf("--check wrote under HOME:\nbefore=%v\nafter=%v", before, after)
		}
		return code, out.String(), errOut.String()
	}
	const okLine = "install check: ok — the install gate would pass\n"
	liveChat := func(t *testing.T, home, account string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(account, "sessions"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(account, "sessions", "4242.json"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(home, "proc", "4242"), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("a migrated host the gate passes answers ok", func(t *testing.T) {
		home, _, _ := installCheckHome(t, false)
		if code, stdout, stderr := check(t, home, ""); code != 0 || stdout != okLine {
			t.Fatalf("code=%d stdout=%q stderr=%q, want 0 and the ok line", code, stdout, stderr)
		}
	})

	t.Run("a legacy host answers ok while no chat is live", func(t *testing.T) {
		home, _, _ := installCheckHome(t, true)
		if _, err := pfmconfig.LoadRuntime(""); !errors.Is(err, pfmconfig.ErrNotMigrated) {
			t.Fatalf("fixture is not legacy: LoadRuntime err=%v, want ErrNotMigrated", err)
		}
		if code, stdout, stderr := check(t, home, ""); code != 0 || stdout != okLine {
			t.Fatalf("code=%d stdout=%q stderr=%q, want 0 and the ok line", code, stdout, stderr)
		}
	})

	t.Run("a legacy host with a live chat exits 4 with the gate's lines", func(t *testing.T) {
		home, clone, account := installCheckHome(t, true)
		liveChat(t, home, account)
		code, stdout, stderr := check(t, home, "")
		wantEnd := "install check: blocked — close what it names, then rerun make -C " + clone + "/pfm host-install\n"
		if code != 4 || stdout != "" || !strings.HasPrefix(stderr, "pfm install: refused before any change:\n") ||
			!strings.Contains(stderr, "  refuse  layout session-store "+account) ||
			!strings.Contains(stderr, "live chats: 4242") || !strings.HasSuffix(stderr, wantEnd) {
			t.Fatalf("code=%d stdout=%q stderr=%q, want 4, the live-chat refusal, %q last",
				code, stdout, stderr, wantEnd)
		}
	})

	t.Run("a pending journal exits 4", func(t *testing.T) {
		home, _, _ := installCheckHome(t, false)
		id := "20260102T030405Z"
		dir := filepath.Join(home, ".local", "state", "pfm", "migrations", id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		journal := []byte(`[{"row":"config","result":"pending"}]` + "\n")
		if err := os.WriteFile(filepath.Join(dir, "journal.json"), journal, 0o600); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := check(t, home, "")
		want := "  refuse  journal " + id +
			" — pending records from a crashed or failed install: run pfm install --rollback " + id + "\n"
		if code != 4 || stdout != "" || !strings.Contains(stderr, want) {
			t.Fatalf("code=%d stdout=%q stderr=%q, want 4 and %q", code, stdout, stderr, want)
		}
	})

	t.Run("a refusal made only of unreadable rows cannot answer", func(t *testing.T) {
		for name, stage := range map[string]func(t *testing.T, home, account string){
			// A regular file where the sessions directory belongs: the live-chat
			// scan fails, so the session-store finding carries Err.
			"finding": func(t *testing.T, _, account string) {
				if err := os.WriteFile(filepath.Join(account, "sessions"), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			"journal": func(t *testing.T, home, _ string) {
				dir := filepath.Join(home, ".local", "state", "pfm", "migrations", "20260102T030405Z")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "journal.json"), []byte("{not json"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		} {
			t.Run(name, func(t *testing.T) {
				home, _, account := installCheckHome(t, true)
				stage(t, home, account)
				code, stdout, stderr := check(t, home, "")
				if code != 1 || stdout != "" || !strings.Contains(stderr, "UNREADABLE") ||
					!strings.Contains(stderr, "install check: cannot answer — fix what it names") {
					t.Fatalf("code=%d stdout=%q stderr=%q, want 1 naming the unreadable row", code, stdout, stderr)
				}
			})
		}
	})

	t.Run("the apply's space preflight refuses the check", func(t *testing.T) {
		previousSpace := checkInstallSpace
		t.Cleanup(func() { checkInstallSpace = previousSpace })
		checkInstallSpace = func(installer.LayoutEnv, []installer.LayoutFinding, []string) error {
			return errors.New("space: /disk needs 9 GiB, has 1 GiB")
		}
		home, _, _ := installCheckHome(t, true)
		code, stdout, stderr := check(t, home, "")
		if code != 1 || stdout != "" || stderr != "pfm install: space: /disk needs 9 GiB, has 1 GiB\n" {
			t.Fatalf("code=%d stdout=%q stderr=%q, want 1 and the space refusal", code, stdout, stderr)
		}
	})

	t.Run("an explicit config that does not exist refuses the check", func(t *testing.T) {
		home, _, _ := installCheckHome(t, false)
		missing := filepath.Join(home, "missing.json")
		code, stdout, stderr := check(t, home, missing)
		if code != 1 || stdout != "" || !strings.Contains(stderr, "--config "+missing+" does not exist") {
			t.Fatalf("code=%d stdout=%q stderr=%q, want 1 and the missing-config refusal", code, stdout, stderr)
		}
	})
}
