package config

import (
	"fmt"
	"path/filepath"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// Account projections: the per-engine views of the roster that the fleet scan,
// the picker and the runtime loader each need. They live here, beside the
// roster itself, so no caller re-derives them.

// CodexHomes lists each Codex account's home directory, in roster order.
func (config Config) CodexHomes() []string {
	result := make([]string, 0, len(config.CodexAccounts))
	for _, account := range config.CodexAccounts {
		result = append(result, account.Home)
	}
	return result
}

// AccountEmojis maps every Claude account id to its emoji.
func (config Config) AccountEmojis() map[int]string {
	result := make(map[int]string, len(config.Accounts))
	for _, account := range config.Accounts {
		result[account.ID] = config.EmojiFor(account.ID)
	}
	return result
}

// CodexAccountEmojis maps every Codex account id to its emoji.
func (config Config) CodexAccountEmojis() map[int]string {
	result := make(map[int]string, len(config.CodexAccounts))
	for _, account := range config.CodexAccounts {
		result[account.ID] = config.CodexEmojiFor(account.ID)
	}
	return result
}

// LabelEmojis lists the Claude account emojis that can label a chat's tmux
// window — every configured emoji except the empty one and the "·" filler.
func (config Config) LabelEmojis() []string {
	result := make([]string, 0, len(config.Accounts))
	for _, account := range config.Accounts {
		if emoji := config.EmojiFor(account.ID); emoji != "" && emoji != "·" {
			result = append(result, emoji)
		}
	}
	return result
}

// PrimaryCodexAccount is the first Codex account's id, or 0 with none.
func (config Config) PrimaryCodexAccount() int {
	if len(config.CodexAccounts) == 0 {
		return 0
	}
	return config.CodexAccounts[0].ID
}

// PrimaryAccountFor is the account a row of engine opens on: Codex and
// OpenCode rows take their own roster's primary, and only a Claude row takes
// claudePrimary — the account pfm's primary-set picked, which names a Claude
// account and means nothing to another engine's roster.
func (config Config) PrimaryAccountFor(engine pfmengine.ID, claudePrimary int) int {
	switch engine {
	case pfmengine.Codex:
		return config.PrimaryCodexAccount()
	case pfmengine.OpenCode:
		return config.PrimaryOpenCodeAccount()
	default:
		return claudePrimary
	}
}

// AccountChoices counts the configured accounts a chat of engine can run on:
// with one, the account a chat runs on is never a guess.
func (config Config) AccountChoices(engine pfmengine.ID) int {
	switch engine {
	case pfmengine.Codex:
		return len(config.CodexAccounts)
	case pfmengine.OpenCode:
		return len(config.OpenCodeAccounts)
	default:
		return len(config.Accounts)
	}
}

// ImplicitAccount prefers account 1, then the first configured account, then 1.
func (config Config) ImplicitAccount() int {
	for _, account := range config.Accounts {
		if account.ID == 1 {
			return 1
		}
	}
	if len(config.Accounts) != 0 {
		return config.Accounts[0].ID
	}
	return 1
}

// AccountForConfigDir returns the Claude account represented by configDir.
func (config Config) AccountForConfigDir(configDir string) int {
	if configDir == "" || len(config.Accounts) == 0 {
		return config.ImplicitAccount()
	}
	if resolved, err := filepath.EvalSymlinks(configDir); err == nil {
		configDir = resolved
	}
	configDir = filepath.Clean(configDir)
	for _, account := range config.Accounts {
		candidate := filepath.Clean(account.ConfigDir)
		if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
			candidate = resolved
		}
		if configDir == candidate {
			return account.ID
		}
	}
	return config.ImplicitAccount()
}

// OpenCodeAccountIDs lists every OpenCode account id, in roster order.
func (config Config) OpenCodeAccountIDs() []int {
	result := make([]int, 0, len(config.OpenCodeAccounts))
	for _, account := range config.OpenCodeAccounts {
		result = append(result, account.ID)
	}
	return result
}

// PrimaryOpenCodeAccount is the first OpenCode account's id, or 0 with none.
func (config Config) PrimaryOpenCodeAccount() int {
	if len(config.OpenCodeAccounts) == 0 {
		return 0
	}
	return config.OpenCodeAccounts[0].ID
}

func validateAccounts(values []rawAccount, home string) ([]Account, error) {
	seen := make(map[int]bool, len(values))
	seenDirs := make(map[string]int, len(values))
	accounts := make([]Account, 0, len(values))
	for index, value := range values {
		if value.ID < 1 {
			return nil, fmt.Errorf("entry %d id must be positive", index+1)
		}
		if seen[value.ID] {
			return nil, fmt.Errorf("duplicate id %d", value.ID)
		}
		seen[value.ID] = true
		configDir, err := expandHomePath(value.ConfigDir, home)
		if err != nil {
			return nil, fmt.Errorf("entry %d configDir: %w", index+1, err)
		}
		configDir = filepath.Clean(configDir)
		if earlier, found := seenDirs[configDir]; found {
			return nil, fmt.Errorf("entry %d configDir %s duplicates entry %d", index+1, configDir, earlier)
		}
		seenDirs[configDir] = index + 1
		accounts = append(accounts, Account{
			ID:        value.ID,
			ConfigDir: configDir,
			Emoji:     DefaultEmoji(value.ID),
		})
	}
	return accounts, nil
}
