// Package claudeplugin reads the records Claude Code keeps about plugins in an
// account config dir — settings.json's enabledPlugins,
// plugins/installed_plugins.json, plugins/known_marketplaces.json and a
// directory marketplace's .claude-plugin/marketplace.json — and resolves a
// plugin's root and its agent definitions from them. The installer, doctor and
// agentrole read those files through it; hostcheck still keeps its own readers
// (function_hooks.go, store.go), to fold here when that code is next touched.
//
// A file that is missing reads as "found nothing"; a file that exists but
// cannot be read or parsed returns its error with its path, never an answer.
package claudeplugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// ConfigDir is the Claude config dir the plugin records are read in:
// CLAUDE_CONFIG_DIR when set and non-empty, else the shared store home/.claude.
func ConfigDir(home string) string {
	if dir := pfmconfig.AmbientClaudeConfigDir(); dir != "" {
		return dir
	}
	return claudelaunch.ClaudeStore(home)
}

// ReadSettings reads the settings.json at path as a document. A missing file is
// an empty document; a file that cannot be read or parsed returns its error.
func ReadSettings(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if document == nil {
		document = map[string]any{}
	}
	return document, nil
}

// Install is one install record Claude Code keeps for a plugin id in
// plugins/installed_plugins.json: one per scope it is installed in, a project
// or local one naming its project. Claude Code owns the shape: a scope,
// projectPath or version that is not a string reads as empty rather than
// failing the record every other reader needs, and an entry whose installPath
// is not one reads as not installed.
type Install struct {
	Scope       string
	ProjectPath string
	InstallPath string
	Version     string
}

// UnmarshalJSON reads one entry, its loose fields as recordString does.
func (install *Install) UnmarshalJSON(raw []byte) error {
	var entry struct {
		Scope       json.RawMessage `json:"scope"`
		ProjectPath json.RawMessage `json:"projectPath"`
		InstallPath json.RawMessage `json:"installPath"`
		Version     json.RawMessage `json:"version"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return err
	}
	*install = Install{
		Scope:       recordString(entry.Scope),
		ProjectPath: recordString(entry.ProjectPath),
		InstallPath: recordString(entry.InstallPath),
		Version:     recordString(entry.Version),
	}
	return nil
}

// recordString is a raw record value as a string; empty when it is absent or
// not a JSON string.
func recordString(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return ""
	}
	return text
}

// ReadInstalls reads configDir's plugins/installed_plugins.json, keyed by
// plugin id. A missing file means none is installed; a file that cannot be read
// or parsed returns its error, never an answer.
func ReadInstalls(configDir string) (map[string][]Install, error) {
	path := filepath.Join(configDir, "plugins", "installed_plugins.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var record struct {
		Plugins map[string][]Install `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return record.Plugins, nil
}

// MarketplaceSource is the source Claude Code records for one marketplace in
// known_marketplaces.json: a github repo, a git or url record's url, or a
// directory's path.
type MarketplaceSource struct {
	Kind string `json:"source"`
	Repo string `json:"repo"`
	Path string `json:"path"`
	URL  string `json:"url"`
}

// The record kinds pfm compares a source with.
const (
	MarketplaceGitHub    = "github"
	MarketplaceDirectory = "directory"
)

// KnownMarketplace is one known_marketplaces.json entry: its source and the
// folder Claude Code reads it from.
type KnownMarketplace struct {
	Source          MarketplaceSource `json:"source"`
	InstallLocation string            `json:"installLocation"`
}

// ReadKnownMarketplaces reads configDir's plugins/known_marketplaces.json,
// keyed by marketplace name, and returns its path beside them. A missing file
// means none is known; a file that cannot be read or parsed returns its error,
// never an answer.
func ReadKnownMarketplaces(configDir string) (string, map[string]KnownMarketplace, error) {
	var known map[string]KnownMarketplace
	path, err := decodeKnownMarketplaces(configDir, &known)
	return path, known, err
}

// KnownMarketplaceNames reads configDir's plugins/known_marketplaces.json for
// the marketplace names alone: an entry of any shape counts as known, so a
// record whose fields ReadKnownMarketplaces cannot type still names its
// marketplace. A missing file means none is known; a file that cannot be read
// or parsed returns its error, never an answer.
func KnownMarketplaceNames(configDir string) (map[string]bool, error) {
	var known map[string]json.RawMessage
	if _, err := decodeKnownMarketplaces(configDir, &known); err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(known))
	for name := range known {
		names[name] = true
	}
	return names, nil
}

// decodeKnownMarketplaces decodes configDir's plugins/known_marketplaces.json
// into into and returns its path; a missing file leaves into untouched.
func decodeKnownMarketplaces(configDir string, into any) (string, error) {
	path := filepath.Join(configDir, "plugins", "known_marketplaces.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return path, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return path, fmt.Errorf("parse %s: %w", path, err)
	}
	return path, nil
}

// Marketplace is a directory marketplace's .claude-plugin/marketplace.json.
type Marketplace struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Owner       *MarketplaceOwner  `json:"owner,omitempty"`
	Plugins     []MarketplaceEntry `json:"plugins"`
}

// MarketplaceOwner is a marketplace manifest's owner.
type MarketplaceOwner struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// MarketplaceEntry's Source is any: Claude also accepts an object source.
type MarketplaceEntry struct {
	Name   string `json:"name"`
	Source any    `json:"source"`
}

// Root is one enabled name@marketplace key of a plugin and the folder Claude
// Code loads it from; Dir is empty when no record names one.
type Root struct {
	ID  string
	Dir string
}

// Roots resolves plugin name in configDir: every name@marketplace key
// settings.json enables, sorted, each with its folder. A directory
// marketplace's folder is its installLocation joined with the path source its
// marketplace.json lists for name; any other marketplace, or a missing piece
// there, falls back to the first installed_plugins.json installPath of the key.
// No enabled key is found nothing (nil, nil); a record that cannot be read or
// parsed returns its error.
func Roots(configDir, name string) ([]Root, error) {
	settings := filepath.Join(configDir, "settings.json")
	document, err := ReadSettings(settings)
	if err != nil {
		return nil, err
	}
	enabled, isObject := document["enabledPlugins"].(map[string]any)
	if value, present := document["enabledPlugins"]; present && value != nil && !isObject {
		return nil, fmt.Errorf("parse %s: enabledPlugins is no object", settings)
	}
	var roots []Root
	for id, value := range enabled {
		if plugin, _, _ := strings.Cut(id, "@"); plugin == name && value == true {
			roots = append(roots, Root{ID: id})
		}
	}
	if len(roots) == 0 {
		return nil, nil
	}
	slices.SortFunc(roots, func(a, b Root) int { return strings.Compare(a.ID, b.ID) })
	_, known, err := ReadKnownMarketplaces(configDir)
	if err != nil {
		return nil, err
	}
	var installs map[string][]Install
	installsRead := false
	for index := range roots {
		_, market, _ := strings.Cut(roots[index].ID, "@")
		if roots[index].Dir, err = directoryRoot(known[market], name); err != nil {
			return nil, err
		}
		if roots[index].Dir != "" {
			continue
		}
		if !installsRead {
			if installs, err = ReadInstalls(configDir); err != nil {
				return nil, err
			}
			installsRead = true
		}
		if entries := installs[roots[index].ID]; len(entries) != 0 {
			roots[index].Dir = entries[0].InstallPath
		}
	}
	return roots, nil
}

// directoryRoot is the folder a directory marketplace's manifest lists for
// plugin name; empty when market is no directory marketplace or its manifest
// is missing or lists no path source for name.
func directoryRoot(market KnownMarketplace, name string) (string, error) {
	if market.Source.Kind != MarketplaceDirectory || market.InstallLocation == "" {
		return "", nil
	}
	path := filepath.Join(market.InstallLocation, ".claude-plugin", "marketplace.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var manifest Marketplace
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	for _, entry := range manifest.Plugins {
		if source, isPath := entry.Source.(string); entry.Name == name && isPath {
			return filepath.Join(market.InstallLocation, source), nil
		}
	}
	return "", nil
}

// AgentFile is the agent definition for agent in the plugin folder root:
// agents/{agent}.md when its frontmatter name is agent or absent, else the
// agents/*.md whose frontmatter name is agent. found is false when none is.
func AgentFile(root, agent string) (path string, found bool, err error) {
	dir := filepath.Join(root, "agents")
	direct := filepath.Join(dir, agent+".md")
	name, err := AgentFrontmatterName(direct)
	switch {
	case err == nil && (name == "" || name == agent):
		return direct, true, nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return "", false, fmt.Errorf("read %s: %w", direct, err)
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		candidate := filepath.Join(dir, entry.Name())
		name, err := AgentFrontmatterName(candidate)
		if err != nil {
			return "", false, fmt.Errorf("read %s: %w", candidate, err)
		}
		if name == agent {
			return candidate, true, nil
		}
	}
	return "", false, nil
}

// AgentFrontmatterName reads the `name:` field out of an agent Markdown file's leading
// YAML frontmatter block, empty when the file has none or the field is absent —
// the same shape engine-sync compiles agent identity from. It scans lines, not
// YAML: the module carries no YAML parser and only this one flat key is read.
func AgentFrontmatterName(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(content), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", nil
	}
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		if name, found := strings.CutPrefix(trimmed, "name:"); found {
			return strings.TrimSpace(name), nil
		}
	}
	return "", nil
}
