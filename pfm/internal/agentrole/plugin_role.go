package agentrole

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudeplugin"
)

// resolvePluginRole reads the constitution of a cc seat's <plugin>:<agent>
// role from the plugin Claude Code loads it from, resolved in configDir's
// plugin records: the plugin's root, then its agent definition. A plugin not
// enabled, a record that could not be read and an enabled plugin without the
// agent are three different errors.
func resolvePluginRole(configDir, role, plugin, agent string) (string, Artifact, error) {
	if plugin == "" || agent == "" || strings.ContainsAny(plugin, `/\`) || strings.ContainsAny(agent, `/\:`) {
		return "", Artifact{}, fmt.Errorf("agent role %q: want <plugin>:<agent>, each one name", role)
	}
	roots, err := claudeplugin.Roots(configDir, plugin)
	if err != nil {
		return "", Artifact{}, fmt.Errorf(
			"agent role %q: could not read the records of plugin %s: %w",
			role,
			plugin,
			err,
		)
	}
	if len(roots) == 0 {
		return "", Artifact{}, fmt.Errorf("agent role %q: plugin %s is not enabled in %s",
			role, plugin, filepath.Join(configDir, "settings.json"))
	}
	var searched []string
	for _, root := range roots {
		if root.Dir == "" {
			searched = append(searched, root.ID+": no plugin folder recorded")
			continue
		}
		path, found, err := claudeplugin.AgentFile(root.Dir, agent)
		if err != nil {
			return "", Artifact{}, fmt.Errorf("agent role %q: %w", role, err)
		}
		if !found {
			searched = append(searched, root.ID+": "+filepath.Join(root.Dir, "agents")+" has no agent "+agent)
			continue
		}
		text, err := readMarkdownConstitution(path)
		if err != nil {
			return "", Artifact{}, err
		}
		return text, Artifact{Path: path}, nil
	}
	return "", Artifact{}, errors.New("agent role: no claude role \"" + role + "\" found\n  searched " +
		strings.Join(searched, "\n  searched "))
}
