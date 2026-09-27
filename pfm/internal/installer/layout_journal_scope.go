package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

type layoutJournalAccount struct {
	ID        int    `json:"id"`
	ConfigDir string `json:"configDir"`
	Implicit  bool   `json:"implicit"`
}

type layoutJournalScope struct {
	Accounts   []layoutJournalAccount `json:"accounts"`
	CodexHomes []string               `json:"codexHomes"`
	StateDB    string                 `json:"stateDb"`
	CacheDB    string                 `json:"cacheDb"`
}

func writeLayoutJournalScope(dir string, env LayoutEnv) error {
	scope := layoutJournalScope{
		Accounts:   make([]layoutJournalAccount, 0, len(env.Config.Accounts)),
		CodexHomes: env.Config.CodexHomes(),
		StateDB:    env.StateDB,
		CacheDB:    env.CacheDB,
	}
	for _, account := range env.Config.Accounts {
		scope.Accounts = append(scope.Accounts, layoutJournalAccount{
			ID: account.ID, ConfigDir: account.ConfigDir, Implicit: account.Implicit,
		})
	}
	if scope.CodexHomes == nil {
		scope.CodexHomes = []string{}
	}
	raw, err := json.Marshal(scope)
	if err != nil {
		return fmt.Errorf("encode journal scope: %w", err)
	}
	if err := atomicfile.Write(filepath.Join(dir, "scope.json"), append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write journal scope: %w", err)
	}
	return nil
}

func readLayoutJournalScope(dir, id string) (layoutJournalScope, error) {
	path := filepath.Join(dir, "scope.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return layoutJournalScope{}, fmt.Errorf("rollback %s refused: journal %s has no scope.json", id, dir)
	}
	if err != nil {
		return layoutJournalScope{}, fmt.Errorf("rollback %s refused: journal scope %s: %w", id, path, err)
	}
	var scope layoutJournalScope
	if err := json.Unmarshal(raw, &scope); err != nil {
		return layoutJournalScope{}, fmt.Errorf("rollback %s refused: journal scope %s: %w", id, path, err)
	}
	if err := scope.validate(); err != nil {
		return layoutJournalScope{}, fmt.Errorf("rollback %s refused: journal scope %s: %w", id, path, err)
	}
	return scope, nil
}

func (scope layoutJournalScope) validate() error {
	check := func(field, value string) error {
		if value == "" {
			return nil
		}
		if !filepath.IsAbs(value) {
			return fmt.Errorf("%s %s is not absolute", field, value)
		}
		if filepath.Clean(value) != value {
			return fmt.Errorf("%s %s is not clean", field, value)
		}
		return nil
	}
	for index, account := range scope.Accounts {
		field := fmt.Sprintf("accounts[%d].configDir", index)
		if account.ConfigDir == "" && !account.Implicit {
			return fmt.Errorf("%s is empty", field)
		}
		if err := check(field, account.ConfigDir); err != nil {
			return err
		}
	}
	for index, home := range scope.CodexHomes {
		if err := check(fmt.Sprintf("codexHomes[%d]", index), home); err != nil {
			return err
		}
	}
	for _, path := range []struct{ field, value string }{
		{"stateDb", scope.StateDB}, {"cacheDb", scope.CacheDB},
	} {
		if err := check(path.field, path.value); err != nil {
			return err
		}
	}
	return nil
}

func (scope layoutJournalScope) apply(env LayoutEnv) LayoutEnv {
	env.Config.Accounts = make([]pfmconfig.Account, 0, len(scope.Accounts))
	for _, account := range scope.Accounts {
		env.Config.Accounts = append(env.Config.Accounts, pfmconfig.Account{
			ID: account.ID, ConfigDir: account.ConfigDir, Implicit: account.Implicit,
		})
	}
	env.Config.CodexAccounts = make([]pfmconfig.CodexAccount, 0, len(scope.CodexHomes))
	for index, home := range scope.CodexHomes {
		env.Config.CodexAccounts = append(env.Config.CodexAccounts, pfmconfig.CodexAccount{ID: index + 1, Home: home})
	}
	env.StateDB, env.CacheDB = scope.StateDB, scope.CacheDB
	return env
}
