package update

import (
	"path"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/retired"
)

// RetiredRegistryNames derives from names the names the registry
// ownership scan matches against an unmarked entry: an agent by file stem, a
// command by its path below the commands registry (`/qa:live` is
// qa/live), a skill by directory name (`skills/transcript` is transcript). A
// name that is not one registry entry (a phrase, an MCP tool, a path) is left out.
func RetiredRegistryNames(names []retired.Name) installer.RetiredRegistryNames {
	registry := installer.RetiredRegistryNames{
		Agents: map[string]bool{}, Commands: map[string]bool{}, Skills: map[string]bool{},
	}
	for _, entry := range names {
		name := entry.Name
		if entry.Kind == retired.KindAgent && entry.Probe != "" {
			name = strings.TrimSuffix(path.Base(entry.Probe), ".md")
		}
		if strings.ContainsAny(name, " ") {
			continue
		}
		switch entry.Kind {
		case retired.KindAgent:
			if !strings.ContainsAny(name, "/:") {
				registry.Agents[name] = true
			}
		case retired.KindCommand:
			if command, found := strings.CutPrefix(name, "/"); found && command != "" {
				registry.Commands[strings.ReplaceAll(strings.TrimSuffix(command, ":"), ":", "/")] = true
			}
		case retired.KindSkill:
			skill := strings.TrimPrefix(name, "skills/")
			if !strings.ContainsAny(skill, "/:") {
				registry.Skills[skill] = true
			}
		}
	}
	return registry
}
