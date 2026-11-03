package installer

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

//go:embed assets
var embeddedAssets embed.FS

type assetFile struct {
	path string
	mode fs.FileMode
}

func assetFiles(sched scheduler) ([]assetFile, error) {
	var files []assetFile
	err := fs.WalkDir(embeddedAssets, "assets", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative := strings.TrimPrefix(name, "assets/")
		if relative == "shim/pfm.zsh" || relative == herdrShellAsset {
			return nil
		}
		if !schedulerAsset(sched, relative) {
			// The other platform's scheduler files are embedded (one binary
			// serves both) but never staged: an operator on macOS should not
			// find a ~/.config/systemd/user of units nothing will read, and an
			// operator on Linux should not find a launch agent.
			return nil
		}
		mode := fs.FileMode(0o644)
		// Everything staged under assets/bin/ is a POSIX launcher or overlay
		// script materialized straight onto disk and exec'd — bin/claude,
		// bin/pfm-statusline, bin/tmux-title-renudge — so the whole
		// directory is executable, not one hand-picked name at a time.
		if path.Ext(relative) == ".sh" || strings.HasPrefix(relative, "bin/") {
			mode = 0o755
		}
		files = append(files, assetFile{path: relative, mode: mode})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(left, right int) bool {
		return files[left].path < files[right].path
	})
	return files, nil
}

// schedulerAsset reports whether an embedded asset belongs under sched. The
// launch agent is never staged: launchd refuses a symlinked plist, so
// wireLaunchAgent writes a real file into ~/Library/LaunchAgents instead of
// linking one out of the managed root.
func schedulerAsset(sched scheduler, relative string) bool {
	switch {
	case strings.HasPrefix(relative, "systemd/"):
		return sched.stagesUnits()
	case strings.HasPrefix(relative, "launchd/"):
		return false
	default:
		return true
	}
}

func mcpSchedulerAsset(relative string) bool {
	return relative == "systemd/pfm-mcp.service" || relative == "launchd/com.professor.pfm.mcp.plist"
}

// renderReloadCommandAsset replaces the {{RELOAD_USAGE}} token in the
// `/reload` command card's frontmatter description with reload.Usage itself
// — the picker then shows the human EXACTLY the flags `reload.Run` accepts,
// never a hand-maintained restatement free to drift from them.
func renderReloadCommandAsset(content []byte) ([]byte, error) {
	folded := foldReloadUsage(reload.Usage)
	escaped := strings.ReplaceAll(folded, "'", "''")
	rendered, err := replaceSingleAssetMarker(string(content), "{{RELOAD_USAGE}}", escaped)
	if err != nil {
		return nil, err
	}
	return []byte(rendered), nil
}

// foldReloadUsage collapses reload.Usage's second, indented continuation line
// into the first. A YAML single-quoted scalar can carry a literal newline,
// but the picker renders a command's description on one line, so a raw
// newline there would show as the two literal characters "\n", not a break —
// folding every newline plus its following indent down to a single space
// keeps the frontmatter both valid YAML and readable in the picker.
func foldReloadUsage(usage string) string {
	lines := strings.Split(usage, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimLeft(line, " ")
	}
	return strings.Join(lines, " ")
}

// servicePathMarker is where every pfm service unit — launchd agent or systemd
// unit — takes its PATH; serviceMarkMarker is where it takes this install's
// mark (paths.EnvInstallMark), so the pfm a unit starts is provably this
// install's when a later stop looks at it.
const (
	servicePathMarker = "__PFM_SERVICE_PATH__"
	serviceMarkMarker = "__PFM_INSTALL_MARK__"
)

// renderServiceEnv fills a unit's servicePathMarker and serviceMarkMarker;
// a unit without a PATH marker is returned unchanged, and one with a PATH but
// no mark is an error, since every unit that starts pfm carries both.
func renderServiceEnv(content []byte, home string) ([]byte, error) {
	names, err := servicemanager.CurrentUnitNames()
	if err != nil {
		return nil, err
	}
	content = []byte(names.Render(string(content)))
	if !strings.Contains(string(content), servicePathMarker) {
		return content, nil
	}
	rendered, err := replaceSingleAssetMarker(string(content), servicePathMarker, paths.ServicePath(home))
	if err != nil {
		return nil, err
	}
	rendered, err = replaceSingleAssetMarker(rendered, serviceMarkMarker, paths.InstallMark(home))
	if err != nil {
		return nil, err
	}
	if prefix := names.Prefix(); prefix != "" {
		if strings.Contains(rendered, "Environment=PFM_INSTALL_MARK=") {
			rendered = strings.Replace(
				rendered,
				"Environment=PFM_INSTALL_MARK=",
				"Environment="+paths.EnvUnitPrefix+"="+prefix+"\nEnvironment=PFM_INSTALL_MARK=",
				1,
			)
		} else {
			rendered = strings.Replace(
				rendered,
				"<key>"+paths.EnvInstallMark+"</key>",
				"<key>"+paths.EnvUnitPrefix+"</key>\n\t\t<string>"+prefix+"</string>\n\t\t<key>"+paths.EnvInstallMark+"</key>",
				1,
			)
		}
	}
	return []byte(rendered), nil
}

func replaceSingleAssetMarker(content, marker, replacement string) (string, error) {
	if count := strings.Count(content, marker); count != 1 {
		return "", fmt.Errorf("asset marker %q occurs %d times, want exactly once", marker, count)
	}
	return strings.Replace(content, marker, replacement, 1), nil
}

// readAsset reads one embedded asset by its managed-root-relative path.
func readAsset(name string) ([]byte, error) {
	return embeddedAssets.ReadFile(path.Join("assets", name))
}
