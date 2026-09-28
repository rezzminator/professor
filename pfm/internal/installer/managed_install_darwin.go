package installer

import "path/filepath"

// managedInstallArgs is the argv sequence, each run under sudo, that installs
// source at destination with mode 0644 and creates its parent. BSD install has
// no -D, so the parent is made first.
func managedInstallArgs(source, destination string) [][]string {
	return [][]string{
		{"mkdir", "-p", filepath.Dir(destination)},
		{installProgram, "-m", "0644", source, destination},
	}
}
