package update

import (
	"path"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/installer"
)

// RetiredRegistryNames derives from retiredNames the names the registry
// ownership scan matches against an unmarked entry: an agent by file stem, a
// command by its path below the commands registry (`/qa:live` is
// qa/live), a skill by directory name (`skills/transcript` is transcript). A
// name that is not one registry entry (a phrase, an MCP tool, a path) is left out.
func RetiredRegistryNames() installer.RetiredRegistryNames {
	names := installer.RetiredRegistryNames{
		Agents: map[string]bool{}, Commands: map[string]bool{}, Skills: map[string]bool{},
	}
	for _, retired := range retiredNames {
		name := retired.name
		if retired.kind == retiredAgent && retired.probe != "" {
			name = strings.TrimSuffix(path.Base(retired.probe), ".md")
		}
		if strings.ContainsAny(name, " ") {
			continue
		}
		switch retired.kind {
		case retiredAgent:
			if !strings.ContainsAny(name, "/:") {
				names.Agents[name] = true
			}
		case retiredCommand:
			if command, found := strings.CutPrefix(name, "/"); found && command != "" {
				names.Commands[strings.ReplaceAll(strings.TrimSuffix(command, ":"), ":", "/")] = true
			}
		case retiredSkill:
			skill := strings.TrimPrefix(name, "skills/")
			if !strings.ContainsAny(skill, "/:") {
				names.Skills[skill] = true
			}
		}
	}
	return names
}
