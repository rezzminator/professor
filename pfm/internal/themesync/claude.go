package themesync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// surfaceClaude is Claude Code's row: the read-only source of the theme,
// named after its engine.
var surfaceClaude = pfmengine.MustLookup(pfmengine.Claude).LongName

// customPrefix starts a theme value naming a file in the account's themes dir.
const customPrefix = "custom:"

// account is one Claude account's settings.json: its real path, the `theme`
// it holds and when the file last changed.
type account struct {
	Path    string
	Value   string
	ModTime time.Time
}

// dir is the account's config directory.
func (a account) dir() string { return filepath.Dir(a.Path) }

// ErrNoAccounts is accountSettings finding no account settings.json at all.
var ErrNoAccounts = errors.New("no Claude account settings.json under ~/.claude or ~/.cc")

// accountSettings lists every account settings.json — ~/.claude and the
// ~/.cc/* config dirs — by real path, deduped and sorted. None is
// ErrNoAccounts.
func accountSettings(home string) ([]string, error) {
	candidates := []string{filepath.Join(home, ".claude", "settings.json")}
	matches, err := filepath.Glob(filepath.Join(home, ".cc", "*", "settings.json"))
	if err != nil {
		return nil, fmt.Errorf("themesync: list account settings: %w", err)
	}
	candidates = append(candidates, matches...)
	seen := map[string]bool{}
	var paths []string
	for _, candidate := range candidates {
		resolved, err := filepath.EvalSymlinks(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("themesync: resolve %s: %w", candidate, err)
		}
		if !seen[resolved] {
			seen[resolved] = true
			paths = append(paths, resolved)
		}
	}
	if len(paths) == 0 {
		return nil, ErrNoAccounts
	}
	sort.Strings(paths)
	return paths, nil
}

// readAccount reads the `theme` key of one settings.json; an absent key is
// "" (Claude's dark default). An unreadable or unparsable file is an error
// naming it.
func readAccount(path string) (account, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return account{}, fmt.Errorf("themesync: read %s: %w", path, err)
	}
	var settings struct {
		Theme string `json:"theme"`
	}
	if err := json.Unmarshal(content, &settings); err != nil {
		return account{}, fmt.Errorf("themesync: parse %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return account{}, fmt.Errorf("themesync: stat %s: %w", path, err)
	}
	return account{Path: path, Value: settings.Theme, ModTime: info.ModTime()}, nil
}

// AccountsSelecting lists the settings.json of every account whose `theme`
// is value, sorted; no account at all is none. An account it cannot read is
// an error naming it, never "not selected".
func AccountsSelecting(home, value string) ([]string, error) {
	accounts, err := readAccounts(home)
	if errors.Is(err, ErrNoAccounts) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var selecting []string
	for _, read := range accounts {
		if read.Value == value {
			selecting = append(selecting, read.Path)
		}
	}
	return selecting, nil
}

// CustomValue is the `theme` value selecting the custom theme file stem.
func CustomValue(stem string) string { return customPrefix + stem }

// readAccounts reads every account; the first failure names its file.
func readAccounts(home string) ([]account, error) {
	paths, err := accountSettings(home)
	if err != nil {
		return nil, err
	}
	accounts := make([]account, 0, len(paths))
	for _, path := range paths {
		read, err := readAccount(path)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, read)
	}
	return accounts, nil
}

// govern picks the account the global surfaces follow (D9): the one whose
// `theme` value changed most recently against the remembered values (a new
// account has no memory, so it is no change); with no change, the account
// that governed before while it is still there; with none, the common value
// when all agree (the first account) else the newest settings.json. It also
// returns the values to remember.
func govern(memory map[string]string, previous string, accounts []account) (account, map[string]string) {
	next := make(map[string]string, len(accounts))
	var changed []account
	for _, candidate := range accounts {
		next[candidate.Path] = candidate.Value
		if old, known := memory[candidate.Path]; known && old != candidate.Value {
			changed = append(changed, candidate)
		}
	}
	newest := func(list []account) account {
		best := list[0]
		for _, candidate := range list[1:] {
			if candidate.ModTime.After(best.ModTime) {
				best = candidate
			}
		}
		return best
	}
	if len(changed) > 0 {
		return newest(changed), next
	}
	for _, candidate := range accounts {
		if candidate.Path == previous {
			return candidate, next
		}
	}
	for _, candidate := range accounts[1:] {
		if candidate.Value != accounts[0].Value {
			return newest(accounts), next
		}
	}
	return accounts[0], next
}

// customFile is Claude Code's custom theme file: {name, base, overrides}.
type customFile struct {
	Name      string            `json:"name"`
	Base      string            `json:"base"`
	Overrides map[string]string `json:"overrides"`
}

// resolved is a theme value turned into its Applied, with the custom theme
// file it read (if any) and that file's mtime.
type resolved struct {
	Applied   Applied
	ThemeFile string
	ThemeTime time.Time
}

// builtinVariant derives a built-in theme; an unknown name is dark, Claude's
// own default branch (D10).
func builtinVariant(name string) (Variant, Appearance, error) {
	tokens, ok := builtinThemes[name]
	if !ok {
		name = builtinDefault
		tokens = builtinThemes[name]
	}
	side := Dark
	if strings.HasPrefix(name, string(Light)) {
		side = Light
	}
	variant, err := deriveVariant(name, tokens)
	return variant, side, err
}

// resolveValue turns a settings.json `theme` value into its Applied: a
// built-in name, `auto` (both sides) or `custom:{name}` read from the
// account's themes dir. A custom file that cannot be read or parsed is an
// error, never a fallback palette.
func resolveValue(value, configDir string) (resolved, error) {
	if value == "" {
		value = builtinDefault
	}
	switch {
	case value == "auto":
		dark, _, err := builtinVariant("dark")
		if err != nil {
			return resolved{}, err
		}
		light, _, err := builtinVariant("light")
		if err != nil {
			return resolved{}, err
		}
		return resolved{
			Applied: Applied{Value: value, Title: value, Auto: true, Side: Dark, Primary: dark, Alternate: &light},
		}, nil
	case strings.HasPrefix(value, customPrefix):
		return resolveCustom(value, configDir)
	}
	variant, side, err := builtinVariant(value)
	if err != nil {
		return resolved{}, err
	}
	return resolved{Applied: Applied{Value: value, Title: value, Side: side, Primary: variant}}, nil
}

func resolveCustom(value, configDir string) (resolved, error) {
	name := strings.TrimPrefix(value, customPrefix)
	if name == "" || strings.ContainsAny(name, `/\`) {
		return resolved{}, fmt.Errorf(
			"custom theme %s: the name is empty or holds a path separator (a theme is a file directly in %s)",
			name, filepath.Join(configDir, "themes"),
		)
	}
	path := filepath.Join(configDir, "themes", name+".json")
	// A FIFO or device in place of the file would block the read, and the
	// follower holds the theme lock while it reads; a file Stat cannot see is
	// named by the read's own error below.
	if info, err := os.Stat(path); err == nil && !info.Mode().IsRegular() {
		return resolved{}, fmt.Errorf("custom theme %s: %s is not a regular file (%s)", name, path, info.Mode().Type())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return resolved{}, fmt.Errorf("custom theme %s: %w", name, err)
	}
	var file customFile
	if err := json.Unmarshal(content, &file); err != nil {
		return resolved{}, fmt.Errorf("custom theme %s: parse %s: %w", name, path, err)
	}
	base := file.Base
	if _, ok := builtinThemes[base]; !ok {
		base = builtinDefault
	}
	tokens := map[string]string{}
	for token, colour := range builtinThemes[base] {
		tokens[token] = colour
	}
	for token, colour := range file.Overrides {
		tokens[token] = colour
	}
	variant, err := deriveVariant(value, tokens)
	if err != nil {
		return resolved{}, fmt.Errorf("custom theme %s: %w", name, err)
	}
	side := Dark
	if strings.HasPrefix(base, string(Light)) {
		side = Light
	}
	title := file.Name
	if title == "" {
		title = value
	}
	info, err := os.Stat(path)
	if err != nil {
		return resolved{}, fmt.Errorf("custom theme %s: %w", name, err)
	}
	return resolved{
		Applied:   Applied{Value: value, Title: title, Side: side, Primary: variant},
		ThemeFile: path, ThemeTime: info.ModTime(),
	}, nil
}

// themesDir is the account's themes dir by real path when it exists.
func themesDir(configDir string) string {
	dir := filepath.Join(configDir, "themes")
	if resolvedDir, err := filepath.EvalSymlinks(dir); err == nil {
		return resolvedDir
	}
	return dir
}
