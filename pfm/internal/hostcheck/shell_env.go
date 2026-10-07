package hostcheck

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

const checkShellClaudeEnv = "shell-claude-env"

// shellClaudeEnv warns for each Claude Code variable pfm's environment
// exports that an account's settings env lacks. Chats `pfm mcp serve` spawns
// run under launchd or systemd and inherit no login-shell export, so such a
// variable reaches terminal chats only. Names every pfm launch sets itself are
// not compared: each chat carries them whoever spawned it. Inside a Claude
// chat the environment is the chat's, so the check reports it did not look.
func shellClaudeEnv(env Env) ([]Row, error) {
	exported := map[string]string{}
	for _, entry := range env.Environ {
		name, value, _ := strings.Cut(entry, "=")
		exported[name] = value
	}
	if exported[claudelaunch.SessionMarkerEnv] != "" {
		return []Row{{
			Warn,
			checkShellClaudeEnv,
			"login shell",
			"not checked: pfm runs inside a Claude Code chat (" + claudelaunch.SessionMarkerEnv +
				" is set), whose environment is the chat's, not the login shell's",
			"run pfm doctor from a terminal to compare the login shell's " + familyText() +
				" exports with each account's settings env",
		}}, nil
	}
	launchSet := map[string]bool{}
	for _, name := range append(claudelaunch.LaunchEnvNames(), claudelaunch.RuntimeEnvNames()...) {
		launchSet[name] = true
	}
	var shellOnly []string
	for name, value := range exported {
		if value != "" && !launchSet[name] && inClaudeFamily(name) {
			shellOnly = append(shellOnly, name)
		}
	}
	if len(shellOnly) == 0 {
		return nil, nil
	}
	sort.Strings(shellOnly)
	var rows []Row
	var settingsFiles []string
	for _, dir := range accountConfigDirs(env) {
		settingsFiles = append(settingsFiles, filepath.Join(dir, "settings.json"))
	}
	for _, path := range uniquePaths(settingsFiles) {
		settingsEnv, ok := readSettingsEnv(&rows, path)
		if !ok {
			continue
		}
		for _, name := range shellOnly {
			if _, present := settingsEnv[name]; present {
				continue
			}
			rows = append(rows, Row{
				Warn,
				checkShellClaudeEnv,
				path,
				name + " is exported in the login shell but absent from this settings env: chats pfm mcp serve " +
					"spawns under launchd or systemd inherit no login-shell export, so they run without it " +
					"while terminal chats have it",
				fmt.Sprintf(
					`add %q to the "env" block of %s with the value your shell exports, then start a new chat`,
					name,
					path,
				),
			})
		}
	}
	return rows, nil
}

// readSettingsEnv is path's "env" block; a missing file is an empty one. ok
// is false when the read or parse failed, its row already appended.
func readSettingsEnv(rows *[]Row, path string) (map[string]any, bool) {
	before := len(*rows)
	raw, found := readFile(rows, checkShellClaudeEnv, path)
	if !found {
		return nil, len(*rows) == before
	}
	var settings struct {
		Env map[string]any `json:"env"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		*rows = append(*rows, unparsable(checkShellClaudeEnv, path, err,
			"which variables its env carries is unknown"))
		return nil, false
	}
	return settings.Env, true
}

func inClaudeFamily(name string) bool {
	for _, prefix := range claudelaunch.EnvPrefixes() {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func familyText() string {
	var parts []string
	for _, prefix := range claudelaunch.EnvPrefixes() {
		parts = append(parts, prefix+"*")
	}
	return strings.Join(parts, " / ")
}
