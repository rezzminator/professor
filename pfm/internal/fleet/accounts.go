package fleet

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// PrimaryAccount resolves the fleet DB's meta row first, then the
// ~/.claude-primary mirror, and maps anything off the roster to the first
// configured account.
//
// Reading the mirror alone is how the picker came up showing a different
// account from the one the launchers used: primary-set writes both, but a
// database restored without the file, or a file left behind by a rollback,
// makes them disagree, and only one of the two is authoritative.
//
// A lookup failure (the database exists but cannot be read) is returned, not
// folded into "no primary set": that fallback silently answered a shared-db
// outage with the roster's first configured account, which reads identically
// to an operator who never set one.
func PrimaryAccount(values paths.Values, configs ...pfmconfig.Config) (int, error) {
	machine := pfmconfig.Defaults(values.Home, values.Roots[pfmengine.Claude])
	if len(configs) != 0 {
		machine = configs[0]
	}
	account, found, err := fleetdb.ClaudePrimaryAccount(context.Background(), values)
	if err != nil {
		return 0, fmt.Errorf("read primary account: %w", err)
	}
	if found {
		if _, exists := machine.Account(account); exists {
			return account, nil
		}
	}
	if len(machine.Accounts) != 0 {
		return machine.Accounts[0].ID, nil
	}
	return 1, nil
}

// SetPrimaryAccount validates the operator-facing roster before committing
// the fleet DB row and statusline mirror as one reported operation.
func SetPrimaryAccount(values paths.Values, machine pfmconfig.Config, account int) error {
	if _, found := machine.Account(account); !found {
		return fmt.Errorf("primary account %d is not in the configured roster", account)
	}
	return fleetdb.SetClaudePrimaryAccount(context.Background(), values, account, clock.Real.Now().Unix())
}

// CurrentSocket is the tmux socket name of the calling process's own server
// ($TMUX), or "" outside tmux.
func CurrentSocket() string {
	return CurrentSocketFrom(paths.OSEnv{})
}

// CurrentSocketFrom applies the TMUX parsing rule over an injected environment.
func CurrentSocketFrom(env paths.Env) string {
	value := env.Get("TMUX")
	if comma := strings.IndexByte(value, ','); comma >= 0 {
		value = value[:comma]
	}
	if value == "" {
		return ""
	}
	return filepath.Base(value)
}

func accountRoots(accounts []pfmconfig.Account) []compose.AccountRoot {
	roots := make([]compose.AccountRoot, 0, len(accounts))
	for _, account := range accounts {
		path := account.ProjectDir
		if resolved, err := filepath.EvalSymlinks(account.ProjectDir); err == nil {
			path = resolved
		} else if absolute, err := filepath.Abs(account.ProjectDir); err == nil {
			path = absolute
		}
		roots = append(roots, compose.AccountRoot{
			Account:   account.ID,
			Path:      filepath.Clean(path),
			ConfigDir: account.ConfigDir,
		})
	}
	return roots
}

func codexAccountRoots(accounts []pfmconfig.CodexAccount) []compose.AccountRoot {
	result := make([]compose.AccountRoot, 0, len(accounts))
	for _, account := range accounts {
		result = append(result, compose.AccountRoot{Account: account.ID, Path: account.Home, ConfigDir: account.Home})
	}
	return result
}
