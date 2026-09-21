package config

import (
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

// AccountForConfigDir returns the Claude account represented by configDir.
func (config Config) AccountForConfigDir(configDir string) int {
	if len(config.Accounts) == 0 {
		return 1
	}
	if configDir == "" {
		for _, account := range config.Accounts {
			if account.Implicit {
				return account.ID
			}
		}
		return config.Accounts[0].ID
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
	return config.Accounts[0].ID
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
