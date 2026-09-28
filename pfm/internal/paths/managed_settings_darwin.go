package paths

// defaultManagedSettingsDir is where Claude Code reads managed-settings.d
// drop-ins on macOS — its only managed-settings location there; the path
// holds a space, so every command naming it quotes it.
const defaultManagedSettingsDir = "/Library/Application Support/ClaudeCode/managed-settings.d"
