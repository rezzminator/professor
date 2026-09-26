package config

import "path/filepath"

// ClaudeConfigDirs is the configured query roster. The implicit account uses
// Claude's default home; transcript roots are independent of these directories.
func (config Config) ClaudeConfigDirs(home string) []string {
	directories := make([]string, 0, len(config.Accounts))
	seen := make(map[string]bool, len(config.Accounts))
	for _, account := range config.Accounts {
		directory := account.ConfigDir
		if account.Implicit {
			directory = filepath.Join(home, ".claude")
		}
		if directory == "" || seen[directory] {
			continue
		}
		seen[directory] = true
		directories = append(directories, directory)
	}
	return directories
}
