package codexgen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func discoverProjects(root string, cfg Config, result *Result) []string {
	projects := []string{"."}
	if cfg.Projects != nil {
		for _, project := range cfg.Projects {
			if project != "." {
				if _, statErr := os.Stat(paths.WorkbenchManifest(filepath.Join(root, project))); statErr == nil {
					result.Warnings = append(
						result.Warnings,
						project+" is a workbench: it builds as its own root, not as a child project",
					)
					continue
				}
			}
			if project != "." && hasClaude(filepath.Join(root, project)) {
				projects = append(projects, project)
			}
		}
	} else if entries, err := os.ReadDir(root); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || excluded(cfg.ExcludeProjects, entry.Name()) || entry.Name() == ".claude" ||
				entry.Name() == ".codex" {
				continue
			}
			if _, statErr := os.Stat(paths.WorkbenchManifest(filepath.Join(root, entry.Name()))); statErr == nil {
				continue
			}
			if hasClaude(filepath.Join(root, entry.Name())) {
				projects = append(projects, entry.Name())
			}
		}
	} else {
		result.Problems = append(result.Problems, fmt.Sprintf("read repository root %s: %v", root, err))
	}
	sort.Strings(projects[1:])
	return projects
}
