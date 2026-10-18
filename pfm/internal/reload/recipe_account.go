package reload

import (
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// RecipeAccount names the configured account a seat's launch recipe runs on:
// the one account whose directory the recipe environment's CLAUDE_CONFIG_DIR
// (Codex: CODEX_HOME) names. false when the environment names no absolute
// directory, none or several configured accounts own it, or the engine has no
// account roster to match, because a guessed account is a reload nobody asked
// for. The one matcher of native reload's default account and the open door's
// account-change offer.
func RecipeAccount(machine pfmconfig.Config, engine pfmengine.ID, env []string) (int, bool) {
	var directory string
	homes := make(map[int]string)
	switch engine {
	case pfmengine.Claude:
		directory = claudelaunch.ConfigDirFromEnv(env)
		for _, account := range machine.Accounts {
			homes[account.ID] = account.ConfigDir
		}
	case pfmengine.Codex:
		directory = nativeReloadEnv(env, "CODEX_HOME")
		for _, account := range machine.CodexAccounts {
			homes[account.ID] = account.Home
		}
	default:
		return 0, false
	}
	if !filepath.IsAbs(directory) {
		return 0, false
	}
	account, matches := 0, 0
	for id, home := range homes {
		if filepath.Clean(home) == filepath.Clean(directory) {
			account = id
			matches++
		}
	}
	if matches != 1 {
		return 0, false
	}
	return account, true
}
