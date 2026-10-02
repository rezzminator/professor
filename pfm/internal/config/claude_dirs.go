package config

// ClaudeConfigDirs is the configured query roster; transcript roots are
// independent of these directories.
func (config Config) ClaudeConfigDirs() []string {
	directories := make([]string, 0, len(config.Accounts))
	seen := make(map[string]bool, len(config.Accounts))
	for _, account := range config.Accounts {
		directory := account.ConfigDir
		if directory == "" || seen[directory] {
			continue
		}
		seen[directory] = true
		directories = append(directories, directory)
	}
	return directories
}
