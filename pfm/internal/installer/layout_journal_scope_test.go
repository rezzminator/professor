package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestLayoutJournalWritesScopeWithFirstRecord(t *testing.T) {
	env := layoutFixture(t)
	env.Config.Accounts = []pfmconfig.Account{
		{ID: 1, ConfigDir: filepath.Join(env.Home, ".claude"), Implicit: true},
		{ID: 2, ConfigDir: filepath.Join(env.Home, "accounts", "2")},
	}
	env.Config.CodexAccounts = []pfmconfig.CodexAccount{{ID: 7, Home: filepath.Join(env.Home, "codex", "7")}}
	journal := NewJournal(context.Background(), env)
	if err := journal.Write([]string{filepath.Join(env.Home, ".zshrc")}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(journal.Dir(), "scope.json"))
	if err != nil {
		t.Fatal(err)
	}
	var scope layoutJournalScope
	if err := json.Unmarshal(raw, &scope); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(scope.Accounts, []layoutJournalAccount{
		{ID: 1, ConfigDir: filepath.Join(env.Home, ".claude"), Implicit: true},
		{ID: 2, ConfigDir: filepath.Join(env.Home, "accounts", "2")},
	}) || !reflect.DeepEqual(scope.CodexHomes, []string{filepath.Join(env.Home, "codex", "7")}) ||
		scope.StateDB != env.StateDB || scope.CacheDB != env.CacheDB {
		t.Fatalf("scope=%+v, want journal env", scope)
	}
	if info, err := os.Stat(filepath.Join(journal.Dir(), "scope.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("scope mode=%v err=%v, want 0600", info, err)
	}
}

func TestLayoutJournalScopeWriteFailurePreventsMutation(t *testing.T) {
	env := layoutFixture(t)
	path := filepath.Join(env.Home, ".zshrc")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	journal := NewJournal(context.Background(), env)
	journal.writeScope = func(string, LayoutEnv) error { return errors.New("scope write failed") }
	actionCalled := false
	err = journal.Write([]string{path}, func() error {
		actionCalled = true
		return os.WriteFile(path, []byte("changed"), 0o600)
	})
	if err == nil || !strings.Contains(err.Error(), "scope write failed") || actionCalled || journal.Dir() != "" {
		t.Fatalf("write err=%v actionCalled=%v journal=%q", err, actionCalled, journal.Dir())
	}
	if got, readErr := os.ReadFile(path); readErr != nil || !reflect.DeepEqual(got, before) {
		t.Fatalf("failed scope write changed %s=%q err=%v", path, got, readErr)
	}
}

func TestLayoutRollbackTrustsJournalAccountScope(t *testing.T) {
	env := layoutFixture(t)
	env.Config.Accounts = nil
	accountsRoot := t.TempDir()
	before := map[string]string{}
	for id := 1; id <= 3; id++ {
		registry := filepath.Join(accountsRoot, "accounts", strconv.Itoa(id), ".claude.json")
		content := `{"mcpServers":{"professor":{"command":"before"}}}`
		layoutWrite(t, registry, content)
		before[registry] = content
		env.Config.Accounts = append(env.Config.Accounts, pfmconfig.Account{ID: id, ConfigDir: filepath.Dir(registry)})
	}
	journal := NewJournal(context.Background(), env)
	for registry := range before {
		path := registry
		if err := journal.mutate(
			LayoutFinding{Row: layoutRowAccountMCP, Verdict: VerdictStrip, Path: path},
			[]string{path},
			func() error {
				return os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o600)
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	rollbackEnv := env
	rollbackEnv.Config.Accounts = nil
	if err := RollbackLayout(
		context.Background(),
		rollbackEnv,
		filepath.Base(journal.Dir()),
		false,
		io.Discard,
	); err != nil {
		t.Fatal(err)
	}
	for registry, want := range before {
		if got, err := os.ReadFile(registry); err != nil || string(got) != want {
			t.Errorf("%s restored=%q err=%v, want %q", registry, got, err, want)
		}
	}
}

func TestLayoutRollbackTrustsJournalCodexHomes(t *testing.T) {
	env := layoutFixture(t)
	home := filepath.Join(t.TempDir(), "codex", "1")
	env.Config.CodexAccounts = []pfmconfig.CodexAccount{{ID: 7, Home: home}}
	path := filepath.Join(home, "config.toml")
	layoutWrite(t, path, "before\n")
	journal := NewJournal(context.Background(), env)
	if err := journal.Write([]string{path}, func() error {
		return os.WriteFile(path, []byte("installed\n"), 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	rollbackEnv := env
	rollbackEnv.Config.CodexAccounts = nil
	if err := RollbackLayout(
		context.Background(),
		rollbackEnv,
		filepath.Base(journal.Dir()),
		false,
		io.Discard,
	); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "before\n" {
		t.Fatalf("codex config restored=%q err=%v", got, err)
	}
}

func TestLayoutRollbackTrustsJournalDatabaseScopeForHolderAndRestore(t *testing.T) {
	env := layoutFixture(t)
	oldState := env.StateDB
	env.StateDB = filepath.Join(env.Home, "custom", "state.db")
	layoutWrite(t, env.StateDB, "before")
	journal := NewJournal(context.Background(), env)
	if err := journal.snapshot(layoutRowStateDB, VerdictMove, env.StateDB); err != nil {
		t.Fatal(err)
	}
	fd := filepath.Join(env.ProcRoot, "4242", "fd", "3")
	if err := os.MkdirAll(filepath.Dir(fd), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(env.StateDB, fd); err != nil {
		t.Fatal(err)
	}
	rollbackEnv := env
	rollbackEnv.StateDB = oldState
	id := filepath.Base(journal.Dir())
	if err := RollbackLayout(
		context.Background(),
		rollbackEnv,
		id,
		false,
		io.Discard,
	); err == nil ||
		!strings.Contains(err.Error(), "database held by pid 4242") {
		t.Fatalf("holder check on journal state db: %v", err)
	}
	if err := os.Remove(fd); err != nil {
		t.Fatal(err)
	}
	if err := RollbackLayout(context.Background(), rollbackEnv, id, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(env.StateDB); err != nil || string(got) != "before" {
		t.Fatalf("state db restored=%q err=%v", got, err)
	}
}

func TestLayoutRollbackRefusesMissingOrInvalidScopeBeforeRestore(t *testing.T) {
	for _, scenario := range []struct {
		name, scope, want string
	}{
		{"missing", "", "has no scope.json"},
		{"undecodable", "{", "journal scope"},
		{"relative account", `{"accounts":[{"id":2,"configDir":"relative"}]}`, "accounts[0].configDir relative is not absolute"},
		{"relative codex", `{"codexHomes":["relative"]}`, "codexHomes[0] relative is not absolute"},
		{"relative database", `{"stateDb":"relative"}`, "stateDb relative is not absolute"},
		{"unclean cache", `{"cacheDb":"/tmp/a/../b"}`, "cacheDb /tmp/a/../b is not clean"},
		{"empty explicit account", `{"accounts":[{"id":2}]}`, "accounts[0].configDir is empty"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			env := layoutFixture(t)
			path := filepath.Join(env.Home, ".zshrc")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			journal := NewJournal(context.Background(), env)
			if err := journal.Write([]string{path}, func() error {
				return os.WriteFile(path, []byte("installed"), 0o600)
			}); err != nil {
				t.Fatal(err)
			}
			scopePath := filepath.Join(journal.Dir(), "scope.json")
			if scenario.scope == "" {
				err = os.Remove(scopePath)
			} else {
				err = os.WriteFile(scopePath, []byte(scenario.scope), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = RollbackLayout(context.Background(), env, filepath.Base(journal.Dir()), true, io.Discard)
			if err == nil || !strings.Contains(err.Error(), scenario.want) {
				t.Fatalf("rollback err=%v, want %q", err, scenario.want)
			}
			if got, readErr := os.ReadFile(
				path,
			); readErr != nil || string(got) != "installed" ||
				bytes.Equal(got, before) {
				t.Fatalf("refused rollback changed path=%q err=%v", got, readErr)
			}
		})
	}
}

func TestLayoutRollbackAlreadyRolledBackWinsWithoutScope(t *testing.T) {
	env := layoutFixture(t)
	journal := NewJournal(context.Background(), env)
	if err := journal.Write([]string{filepath.Join(env.Home, ".zshrc")}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(journal.Dir(), "scope.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(journal.Dir(), layoutRolledBackMarker),
		[]byte("2026-01-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	err := RollbackLayout(context.Background(), env, filepath.Base(journal.Dir()), false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "refused: already rolled back at 2026-01-01T00:00:00Z") {
		t.Fatalf("rollback err=%v, want already rolled back refusal", err)
	}
}
