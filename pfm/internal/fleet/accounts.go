package fleet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/shared"
)

// PrimaryAccount resolves the fleet DB's meta row first, then the
// ~/.claude-primary mirror, and maps anything off the roster to the first
// configured account.
//
// Reading the mirror alone is how the picker came up showing a different
// account from the one the launchers used: primary-set writes both, but a
// database restored without the file, or a file left behind by a rollback,
// makes them disagree, and only one of the two is authoritative.
func PrimaryAccount(values paths.Values, configs ...pfmconfig.Config) int {
	machine := pfmconfig.Defaults(values.Home, values.Roots[pfmengine.Claude])
	if len(configs) != 0 {
		machine = configs[0]
	}
	account, found := shared.PrimaryAccount(context.Background(), values)
	if found {
		if _, exists := machine.Account(account); exists {
			return account
		}
	}
	if len(machine.Accounts) != 0 {
		return machine.Accounts[0].ID
	}
	return 1
}

// SetPrimaryAccount validates the operator-facing roster before committing
// the fleet DB row and statusline mirror as one reported operation.
func SetPrimaryAccount(values paths.Values, machine pfmconfig.Config, account int) error {
	if _, found := machine.Account(account); !found {
		return fmt.Errorf("primary account %d is not in the configured roster", account)
	}
	return shared.SetPrimaryAccount(context.Background(), values, account, time.Now().Unix())
}

// CurrentSocket is the tmux socket name of the calling process's own server
// ($TMUX), or "" outside tmux.
func CurrentSocket() string {
	value := os.Getenv("TMUX")
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
