package installer

import (
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// VSCodeReloadNote is the line a successful `pfm install --vscode --yes` ends
// its VS Code step on (installer/vscode.go): VS Code reads an extension and its
// contributes.themes only when a window starts, so a linked extension or a
// rendered theme is missing from each open window until it reloads.
const VSCodeReloadNote = "reload every open VS Code window (Developer: Reload Window): " +
	"VS Code reads the extension's contributed colour themes only at window start"

// vscodeProductRootCandidates lists every VS Code product root the professor
// extension can be linked under: the desktop products' and VS Code Server's
// dot directories under home, plus VSCODE_PORTABLE's data directory.
func vscodeProductRootCandidates(home string, env paths.Env) []string {
	roots := []string{
		filepath.Join(home, ".vscode"),
		filepath.Join(home, ".vscode-insiders"),
		filepath.Join(home, ".vscode-oss"),
		filepath.Join(home, ".vscode-server"),
		filepath.Join(home, ".vscode-server-insiders"),
	}
	if portable := env.Get("VSCODE_PORTABLE"); filepath.IsAbs(portable) {
		roots = append(roots, portable)
	}
	return roots
}

// VSCodeProductRoots is every VS Code product root that exists on this host:
// a VS Code install the extension could be linked into.
func VSCodeProductRoots(home string, env paths.Env) []string {
	var found []string
	for _, root := range vscodeProductRootCandidates(home, env) {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			found = append(found, root)
		}
	}
	return found
}
