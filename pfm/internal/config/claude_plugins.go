package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// ClaudePlugin is one claude.plugins entry. ID is a bare plugin name of ours
// ("auto-effort-control": {name}@professor, from the GitHub professor
// marketplace, or on an -alpha build from its checkout under
// claude.pluginCheckoutRoot, else under the source clone's plugins/) or a full
// "name@marketplace" id.
// Source is what `claude plugin marketplace add` takes (owner/repo, a git or
// https URL, a path) for a full id whose marketplace Claude Code does not know
// yet; empty otherwise.
type ClaudePlugin struct {
	ID     string
	Source string
}

// ProfessorMarketplace is the marketplace every plugin of ours installs from:
// a bare name's id is {name}@professor on every build.
const ProfessorMarketplace = "professor"

// LegacyClaudePluginIDs are the ids a plugin of ours installed as before
// {name}@professor: {name}@{name}, its own GitHub marketplace, and
// {name}@{name}-dev, the -alpha checkout copy. pfm install moves each to
// {name}@professor.
func LegacyClaudePluginIDs(name string) []string {
	return []string{name + "@" + name, name + "@" + name + "-dev"}
}

// claudePluginIDPattern is a plugin id: a name, or name@marketplace, each part
// starting with a letter or digit so an id is one safe path element.
var claudePluginIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(@[A-Za-z0-9][A-Za-z0-9._-]*)?$`)

// DefaultClaudePlugins is the list in force when claude.plugins is unset.
func DefaultClaudePlugins() []ClaudePlugin {
	return []ClaudePlugin{
		{ID: "cache-live-control"},
		{ID: "forward-compaction"},
		{ID: "auto-effort-control"},
		{ID: "tool-visibility-controller"},
		{ID: "engine-sync"},
	}
}

// Bare reports whether the entry is a bare plugin name.
func (plugin ClaudePlugin) Bare() bool { return !strings.Contains(plugin.ID, "@") }

// Name is the plugin's own name: the whole ID of a bare entry, the part before
// the "@" of a full one.
func (plugin ClaudePlugin) Name() string {
	name, _, _ := strings.Cut(plugin.ID, "@")
	return name
}

// Marketplace is the marketplace a full ID names; empty for a bare entry.
func (plugin ClaudePlugin) Marketplace() string {
	_, marketplace, _ := strings.Cut(plugin.ID, "@")
	return marketplace
}

// defaultClaudePluginIDs is Keys()' default for claude.plugins, as the example file spells it.
func defaultClaudePluginIDs() []string {
	defaults := DefaultClaudePlugins()
	ids := make([]string, 0, len(defaults))
	for _, plugin := range defaults {
		ids = append(ids, plugin.ID)
	}
	return ids
}

// decodeClaudePlugins validates the list entry by entry; an error names the
// entry's index and the reason, never an entry quietly dropped. One marketplace
// has one source: Claude Code registers a marketplace name once, so a second
// source for it could never be installed from.
func decodeClaudePlugins(entries []json.RawMessage) ([]ClaudePlugin, error) {
	plugins := make([]ClaudePlugin, 0, len(entries))
	first := map[string]int{}
	sourced := map[string]int{}
	for index, entry := range entries {
		plugin, err := decodeClaudePlugin(entry)
		if err != nil {
			return nil, fmt.Errorf("%s[%d] %w", KeyClaudePlugins, index, err)
		}
		if earlier, repeated := first[plugin.ID]; repeated {
			return nil, fmt.Errorf(
				"%s[%d] repeats %s[%d]: %s",
				KeyClaudePlugins,
				index,
				KeyClaudePlugins,
				earlier,
				plugin.ID,
			)
		}
		if err := claudePluginCopyClash(plugin, index, first); err != nil {
			return nil, err
		}
		if plugin.Source != "" {
			if earlier, named := sourced[plugin.Marketplace()]; named && plugins[earlier].Source != plugin.Source {
				return nil, fmt.Errorf(
					"%s[%d] marketplace %s has source %s, but %s[%d] gives it %s: one marketplace has one source",
					KeyClaudePlugins, index, plugin.Marketplace(), plugin.Source,
					KeyClaudePlugins, earlier, plugins[earlier].Source,
				)
			}
			if _, named := sourced[plugin.Marketplace()]; !named {
				sourced[plugin.Marketplace()] = index
			}
		}
		first[plugin.ID] = index
		plugins = append(plugins, plugin)
	}
	return plugins, nil
}

// claudePluginCopyClash refuses a bare name listed beside a full id of its
// own, {name}@professor or a legacy id (LegacyClaudePluginIDs): the bare entry
// ensures {name}@professor and moves the legacy ids to it, so the full id
// would be ensured twice or moved away again on every install.
func claudePluginCopyClash(plugin ClaudePlugin, index int, first map[string]int) error {
	name := plugin.Name()
	copies := append([]string{name + "@" + ProfessorMarketplace}, LegacyClaudePluginIDs(name)...)
	if plugin.Bare() {
		for _, id := range copies {
			if earlier, listed := first[id]; listed {
				return fmt.Errorf(
					"%s[%d] %s is a bare name of %s[%d] %s, which it ensures or moves: list the plugin once",
					KeyClaudePlugins, index, plugin.ID, KeyClaudePlugins, earlier, id,
				)
			}
		}
		return nil
	}
	earlier, listed := first[name]
	if !listed || !slices.Contains(copies, plugin.ID) {
		return nil
	}
	return fmt.Errorf(
		"%s[%d] %s is an id of %s[%d] %s, which ensures %s@%s and moves its legacy ids to it: list the plugin once",
		KeyClaudePlugins, index, plugin.ID, KeyClaudePlugins, earlier, name, name, ProfessorMarketplace,
	)
}

// decodeClaudePlugin reads one entry: a JSON string (the id) or an {"id","source"} object.
func decodeClaudePlugin(entry json.RawMessage) (ClaudePlugin, error) {
	trimmed := bytes.TrimSpace(entry)
	var plugin ClaudePlugin
	var source *string
	switch {
	case len(trimmed) > 0 && trimmed[0] == '"':
		if err := json.Unmarshal(trimmed, &plugin.ID); err != nil {
			return ClaudePlugin{}, fmt.Errorf("is not a plugin id string: %w", err)
		}
	case len(trimmed) > 0 && trimmed[0] == '{':
		var object struct {
			ID     string  `json:"id"`
			Source *string `json:"source"`
		}
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&object); err != nil {
			return ClaudePlugin{}, fmt.Errorf(`is not an {"id","source"} object: %w`, err)
		}
		plugin.ID, source = object.ID, object.Source
	default:
		return ClaudePlugin{}, fmt.Errorf(`must be a plugin id string or an {"id","source"} object, got %s`, trimmed)
	}
	if !claudePluginIDPattern.MatchString(plugin.ID) {
		return ClaudePlugin{}, fmt.Errorf("id must be a plugin name or name@marketplace, got %q", plugin.ID)
	}
	if source == nil {
		return plugin, nil
	}
	switch {
	case plugin.Bare():
		return ClaudePlugin{}, fmt.Errorf(
			"source needs a name@marketplace id: a bare name resolves to the professor marketplace or its checkout",
		)
	case *source == "" || strings.HasPrefix(*source, "-") ||
		strings.ContainsFunc(*source, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }):
		return ClaudePlugin{}, fmt.Errorf(`source must be one word that does not start with "-", got %q`, *source)
	}
	plugin.Source = *source
	return plugin, nil
}

// marshalClaudePlugins is claude.plugins as the file spells it: an id string,
// or an {"id","source"} object for an entry carrying a source.
func marshalClaudePlugins(plugins []ClaudePlugin) []any {
	value := make([]any, 0, len(plugins))
	for _, plugin := range plugins {
		if plugin.Source == "" {
			value = append(value, plugin.ID)
			continue
		}
		value = append(value, map[string]any{"id": plugin.ID, "source": plugin.Source})
	}
	return value
}
