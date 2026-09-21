package installer

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	"github.com/rezzminator/professor/pfm/internal/reload"
)

//go:embed assets
var embeddedAssets embed.FS

type assetFile struct {
	path string
	mode fs.FileMode
}

func assetFiles() ([]assetFile, error) {
	files, err := harnessPromptAssetFiles()
	if err != nil {
		return nil, err
	}
	err = fs.WalkDir(embeddedAssets, "assets", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative := strings.TrimPrefix(name, "assets/")
		if !schedulerAsset(relative) {
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

// schedulerAsset reports whether an embedded asset belongs on this platform.
// The launch agent is not staged even on macOS: launchd refuses a symlinked
// plist, so wireLaunchAgent writes a real file into ~/Library/LaunchAgents
// instead of linking one out of the managed root.
func schedulerAsset(relative string) bool {
	switch {
	case strings.HasPrefix(relative, "systemd/"):
		return !schedulerIsLaunchd
	case strings.HasPrefix(relative, "launchd/"):
		return false
	default:
		return true
	}
}

func mcpSchedulerAsset(relative string) bool {
	return relative == "systemd/pfm-mcp.service" || relative == "launchd/com.professor.pfm.mcp.plist"
}

func renderShimAsset(content []byte, options Options) ([]byte, error) {
	codex := []string{"typeset -gA PFM_CODEX_YOLO=("}
	for _, account := range sortedBoolKeys(options.CodexYolo) {
		value := 0
		if options.CodexYolo[account] {
			value = 1
		}
		codex = append(codex, "  ["+strconv.Itoa(account)+"]="+strconv.Itoa(value))
	}
	codex = append(codex, ")")
	text, err := replaceSingleAssetMarker(string(content), "typeset -gA PFM_CODEX_YOLO=()", strings.Join(codex, "\n"))
	if err != nil {
		return nil, err
	}
	return []byte(text), nil
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
// unit — takes its PATH.
const servicePathMarker = "__PFM_SERVICE_PATH__"

// servicePath is the ONE search path every pfm service runs on. A service
// manager starts jobs on a bare PATH (launchd: /usr/bin:/bin:/usr/sbin:/sbin)
// that sees neither ~/.local/bin (pfm, claude) nor Homebrew (/opt/homebrew/bin
// on Apple silicon, /usr/local/bin on Intel), where tmux lives — so a daemon
// that probes the fleet finds no tmux, and a fleet it cannot see reads as
// empty. Every unit renders it from here; none spells a PATH of its own.
func servicePath(home string) string {
	return strings.Join([]string{
		filepath.Join(home, ".local", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	}, ":")
}

// renderServicePath fills a unit's servicePathMarker; a unit without one is
// returned unchanged.
func renderServicePath(content []byte, home string) ([]byte, error) {
	if !strings.Contains(string(content), servicePathMarker) {
		return content, nil
	}
	rendered, err := replaceSingleAssetMarker(string(content), servicePathMarker, servicePath(home))
	if err != nil {
		return nil, err
	}
	return []byte(rendered), nil
}

func replaceSingleAssetMarker(content, marker, replacement string) (string, error) {
	if count := strings.Count(content, marker); count != 1 {
		return "", fmt.Errorf("asset marker %q occurs %d times, want exactly once", marker, count)
	}
	return strings.Replace(content, marker, replacement, 1), nil
}

func sortedBoolKeys(values map[int]bool) []int {
	keys := make([]int, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

// readAsset reads one embedded asset by the managed-root-relative path it
// stages to. The harness-prompt parts are embedded by their own package —
// pfm/harness-prompts, the ONE copy of that tree — and reached through the
// same name, so staging, composition and the command preview all keep one
// door.
func readAsset(name string) ([]byte, error) {
	if relative, isHarnessPrompt := harnessPromptAssetName(name); isHarnessPrompt {
		return harnessprompts.ReadPart(relative)
	}
	return embeddedAssets.ReadFile(path.Join("assets", name))
}
