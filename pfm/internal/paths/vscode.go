package paths

import "path/filepath"

// VSCodeSettingsCandidates lists every VS Code family user settings.json a
// host can hold, the canonical one first: on macOS the desktop products'
// ~/Library/Application Support/{product}/User; elsewhere the same products
// under $XDG_CONFIG_HOME (~/.config when it is not absolute), then each VS
// Code Server's Machine settings, the file a Remote-SSH window on this host
// reads; and on both a portable install's when VSCODE_PORTABLE names its
// data directory. It is the one lookup `pfm install --vscode` and `pfm
// theme` share; which of them exist is the caller's question.
func VSCodeSettingsCandidates(home string, darwin bool, env Env) []string {
	var candidates []string
	if darwin {
		support := filepath.Join(home, "Library", "Application Support")
		candidates = []string{
			filepath.Join(support, "Code", "User", "settings.json"),
			filepath.Join(support, "Code - Insiders", "User", "settings.json"),
			filepath.Join(support, "VSCodium", "User", "settings.json"),
		}
	} else {
		configRoot := env.Get("XDG_CONFIG_HOME")
		if !filepath.IsAbs(configRoot) {
			configRoot = filepath.Join(home, ".config")
		}
		candidates = []string{
			filepath.Join(configRoot, "Code", "User", "settings.json"),
			filepath.Join(configRoot, "Code - Insiders", "User", "settings.json"),
			filepath.Join(configRoot, "VSCodium", "User", "settings.json"),
			filepath.Join(home, ".vscode-server", "data", "Machine", "settings.json"),
			filepath.Join(home, ".vscode-server-insiders", "data", "Machine", "settings.json"),
		}
	}
	if portable := env.Get("VSCODE_PORTABLE"); filepath.IsAbs(portable) {
		// VSCODE_PORTABLE is the portable data directory itself, not the
		// install folder holding it (VS Code's bootstrap-node getPortableDataPath).
		candidates = append(candidates, filepath.Join(portable, "user-data", "User", "settings.json"))
	}
	return candidates
}
