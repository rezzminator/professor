package themesync

import (
	"context"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

// platform is everything a run serves differently per operating system: the
// VS Code settings and servers the vscode row looks for, the process markers
// of a running VS Code, the system appearance probe and the service manager
// the follower runs under. Env.Platform selects one; both run in a Linux test.
type platform struct {
	// vscodeSettings are the settings.json files under home the vscode row looks for.
	vscodeSettings func(home string) []string
	// vscodeServerRoots are the VS Code Server installs under home: a
	// Remote-SSH window's extension host runs from one, and its Machine
	// settings.json exists only once a Remote setting was saved, so the root
	// alone already makes VS Code present (the installer links its extension
	// there on the same evidence).
	vscodeServerRoots func(home string) []string
	// vscodeMarkers identify a running VS Code in a process's argv line.
	vscodeMarkers []string
	// detectAppearance reads the system appearance, or says why it cannot.
	detectAppearance func(context.Context, Env) (Appearance, error)
	// follower is the service manager the follower runs under.
	follower servicemanager.Kind
}

// darwinPlatform keeps the follower's launch agent and each VS Code family
// product's settings under ~/Library/Application Support.
var darwinPlatform = &platform{
	vscodeSettings: func(home string) []string {
		settings := make([]string, 0, len(vscodeProducts))
		for _, product := range vscodeProducts {
			settings = append(
				settings,
				filepath.Join(home, "Library", "Application Support", product, "User", "settings.json"),
			)
		}
		return settings
	},
	vscodeServerRoots: func(string) []string { return nil },
	vscodeMarkers:     vscodeAppMarkers,
	detectAppearance:  detectMacAppearance,
	follower:          servicemanager.KindLaunchd,
}

// linuxPlatform keeps the follower's systemd user unit trio and the settings
// lookup pfm install --vscode shares, the VS Code Server's Machine settings
// included.
var linuxPlatform = &platform{
	vscodeSettings: func(home string) []string {
		return paths.VSCodeSettingsCandidates(home, false, paths.OSEnv{})
	},
	vscodeServerRoots: func(home string) []string {
		return []string{filepath.Join(home, ".vscode-server"), filepath.Join(home, ".vscode-server-insiders")}
	},
	vscodeMarkers:    vscodeLinuxMarkers,
	detectAppearance: unsupportedAppearance,
	follower:         servicemanager.KindSystemd,
}

// platform is the profile the run serves: Env.Platform, else this host's.
func (env Env) platform() *platform {
	if env.Platform == nil {
		return hostPlatform
	}
	return env.Platform
}
