package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// vscodeThemesMarker is the one spot in the extension's embedded package.json
// that stageAssets fills with the rendered contributes.themes entries.
const vscodeThemesMarker = `"themes": []`

// vscodeThemeFilePrefix is the file-name prefix of every VS Code theme pfm
// renders (themesync.RenderVSCodeTheme names them professor-{stem}.json), so
// a stale one is told apart from anything else in the directory.
const vscodeThemeFilePrefix = "professor-"

// vscodeThemesDir is where the rendered themes and their index live: inside
// the staged extension, beside package.json, which names each by a relative
// path.
func (installer *engine) vscodeThemesDir() string {
	return filepath.Join(installer.managedRoot, filepath.FromSlash(vscodeExtensionSource), themesync.VSCodeThemeDir)
}

// renderVSCodeThemes renders one VS Code color theme per profile the
// ownership ledger records as installed, from its whole installed copy
// (palette, pins and VS Code theme layer), so an edit made to it after
// install is the theme VS Code shows. A ledger record of a Claude theme file
// alone has no profile and renders nothing. A theme that will not render is
// skipped by name and path; the rest still render.
func (installer *engine) renderVSCodeThemes() []themesync.VSCodeThemeFile {
	ownershipPath := filepath.Join(installer.managedRoot, themeOwnershipName)
	ownership, err := readThemeOwnership(ownershipPath)
	if err != nil {
		installer.skip("VS Code themes NOT rendered: read ownership " + ownershipPath + ": " + err.Error())
		return nil
	}
	rendered := []themesync.VSCodeThemeFile{}
	for key, profile := range ownership {
		name, isProfile := themeLedgerOwner(key)
		if !isProfile {
			continue
		}
		content, exists, readErr := readOptionalRegularFile(profile.Path)
		switch {
		case readErr != nil:
			installer.skip("VS Code theme " + name + " NOT rendered: " + readErr.Error())
			continue
		case !exists:
			installer.skip("VS Code theme " + name + " NOT rendered: " + profile.Path + " is missing")
			continue
		}
		file, renderErr := themesync.RenderVSCodeTheme(name, content)
		if renderErr != nil {
			installer.skip("VS Code theme " + name + " NOT rendered from " + profile.Path + ": " + renderErr.Error())
			continue
		}
		rendered = append(rendered, file)
	}
	sort.Slice(rendered, func(left, right int) bool {
		if rendered[left].Label != rendered[right].Label {
			return rendered[left].Label < rendered[right].Label
		}
		return rendered[left].ID < rendered[right].ID
	})
	return rendered
}

// vscodeThemeContributions renders the package.json contributes.themes array
// for themes, replacing vscodeThemesMarker: VS Code lists each in its Color
// Theme picker under its label, and workbench.colorTheme names it by its id.
func vscodeThemeContributions(themes []themesync.VSCodeThemeFile) (string, error) {
	if len(themes) == 0 {
		return vscodeThemesMarker, nil
	}
	entries := make([]string, 0, len(themes))
	for _, theme := range themes {
		entry, err := json.Marshal(struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			UITheme string `json:"uiTheme"`
			Path    string `json:"path"`
		}{theme.ID, theme.Label, theme.UITheme, theme.Path})
		if err != nil {
			return "", fmt.Errorf("encode VS Code theme contribution %s: %w", theme.ID, err)
		}
		entries = append(entries, string(entry))
	}
	return `"themes": [` + "\n      " + strings.Join(entries, ",\n      ") + "\n    ]", nil
}

// renderVSCodeExtensionManifest fills the embedded package.json's
// contributes.themes with the rendered themes.
func renderVSCodeExtensionManifest(content []byte, themes []themesync.VSCodeThemeFile) ([]byte, error) {
	contributions, err := vscodeThemeContributions(themes)
	if err != nil {
		return nil, err
	}
	rendered, err := replaceSingleAssetMarker(string(content), vscodeThemesMarker, contributions)
	if err != nil {
		return nil, err
	}
	return []byte(rendered), nil
}

// writeVSCodeThemes writes each rendered theme and the index the extension
// maps a pfm palette to a theme id through, and returns the themes now on
// disk. It runs before stageAssets writes package.json, which lists only
// those, so the manifest never names a theme file that failed to write.
func (installer *engine) writeVSCodeThemes(themes []themesync.VSCodeThemeFile) ([]themesync.VSCodeThemeFile, error) {
	directory := installer.vscodeThemesDir()
	written := make([]themesync.VSCodeThemeFile, 0, len(themes))
	var failures []error
	for _, theme := range themes {
		if err := installer.writeVSCodeThemeFile(
			filepath.Join(directory, filepath.Base(theme.Path)),
			theme.Content,
		); err != nil {
			failures = append(failures, err)
			continue
		}
		written = append(written, theme)
	}
	index, err := themesync.VSCodeThemeIndex(written)
	if err != nil {
		failures = append(failures, fmt.Errorf("encode VS Code theme index: %w", err))
	} else if err := installer.writeVSCodeThemeFile(
		filepath.Join(directory, themesync.VSCodeThemeIndexFile), index,
	); err != nil {
		failures = append(failures, err)
	}
	return written, errors.Join(failures...)
}

func (installer *engine) writeVSCodeThemeFile(target string, content []byte) error {
	if sameFile(target, content, 0o644) {
		installer.ok(target)
		return nil
	}
	if err := installer.change("write "+target, func() error {
		return atomicfile.Write(target, content, 0o644)
	}); err != nil {
		return fmt.Errorf("write VS Code theme %s: %w", target, err)
	}
	return nil
}

// pruneVSCodeThemes removes the rendered themes not in themes — a profile
// removed, a theme uninstalled — once package.json has stopped naming them;
// index also removes the index (uninstall, with themes nil).
func (installer *engine) pruneVSCodeThemes(themes []themesync.VSCodeThemeFile, index bool) error {
	directory := installer.vscodeThemesDir()
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list VS Code themes %s: %w", directory, err)
	}
	keep := make(map[string]bool, len(themes))
	for _, theme := range themes {
		keep[filepath.Base(theme.Path)] = true
	}
	var failures []error
	for _, entry := range entries {
		name := entry.Name()
		stale := strings.HasPrefix(name, vscodeThemeFilePrefix) && strings.HasSuffix(name, ".json") && !keep[name]
		if !stale && (!index || name != themesync.VSCodeThemeIndexFile) {
			continue
		}
		target := filepath.Join(directory, name)
		if changeErr := installer.change("remove "+target, func() error {
			return os.Remove(target)
		}); changeErr != nil {
			failures = append(failures, fmt.Errorf("remove VS Code theme %s: %w", target, changeErr))
		}
	}
	return errors.Join(failures...)
}

// uninstallVSCodeThemes removes every rendered theme, the index and the
// emptied directory, so removeManagedAssets can then drop the extension's
// own directories. A file pfm never wrote keeps the directory, reported as
// a skip, and uninstall goes on.
func (installer *engine) uninstallVSCodeThemes() error {
	if err := installer.pruneVSCodeThemes(nil, true); err != nil {
		return err
	}
	if !installer.apply {
		return nil
	}
	directory := installer.vscodeThemesDir()
	err := os.Remove(directory)
	switch {
	case err == nil, errors.Is(err, fs.ErrNotExist):
		return nil
	case errors.Is(err, fs.ErrExist):
		installer.skip("leave non-empty VS Code theme directory " + directory + ": " + err.Error())
		return nil
	default:
		return fmt.Errorf("remove VS Code theme directory %s: %w", directory, err)
	}
}
