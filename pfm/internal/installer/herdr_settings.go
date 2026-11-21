package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// herdrSettingsDefault is pfm's shipped Herdr settings, config.toml as JSON:
// onboarding false keeps Herdr's first-run onboarding from greeting a
// pfm-managed Herdr, whose setup pfm install already made.
func herdrSettingsDefault() map[string]any {
	return map[string]any{"onboarding": false}
}

// herdrSettingsEffective is herdr.settings over pfm's shipped Herdr settings:
// a table merges key by key, null removes the key, any other value replaces it.
func herdrSettingsEffective(user map[string]any) map[string]any {
	return herdrSettingsMerge(herdrSettingsDefault(), user)
}

func herdrSettingsMerge(base, over map[string]any) map[string]any {
	result := make(map[string]any, len(base))
	maps.Copy(result, base)
	for key, value := range over {
		switch value := value.(type) {
		case nil:
			delete(result, key)
		case map[string]any:
			inner, _ := result[key].(map[string]any)
			result[key] = herdrSettingsMerge(inner, value)
		default:
			result[key] = value
		}
	}
	return result
}

// herdrSetting is one value of the effective Herdr settings, at its table.
type herdrSetting struct {
	table []string
	key   string
	value any
}

// herdrSettingsLeaves are settings' values, each table's in key order, the
// top level first.
func herdrSettingsLeaves(table []string, settings map[string]any) []herdrSetting {
	var leaves, nested []herdrSetting
	for _, key := range slices.Sorted(maps.Keys(settings)) {
		if inner, isTable := settings[key].(map[string]any); isTable {
			nested = append(nested, herdrSettingsLeaves(append(slices.Clone(table), key), inner)...)
			continue
		}
		leaves = append(leaves, herdrSetting{table, key, settings[key]})
	}
	return append(leaves, nested...)
}

// wireHerdrSettings adds each effective Herdr setting the user's config.toml
// lacks, the user's value of a key the file sets always kept.
func (installer *engine) wireHerdrSettings() (result error) {
	if absent, err := installer.herdrAbsent(); err != nil || absent {
		if absent {
			installer.skip("herdr settings: herdr absent")
		}
		return err
	}
	home := installer.options.Home
	if installer.apply {
		release, err := themesync.LockState(home)
		if err != nil {
			return fmt.Errorf("lock Herdr settings config: %w", err)
		}
		defer func() { result = errors.Join(result, release()) }()
	}
	path, err := paths.HerdrConfig(home, installer.env())
	if err != nil {
		return fmt.Errorf("locate Herdr config for its settings: %w", err)
	}
	raw, existed, err := readCodexConfig(path)
	if err != nil {
		return fmt.Errorf("read Herdr settings config: %w", err)
	}
	current, err := decodeHerdrConfig(string(raw))
	if err != nil {
		return fmt.Errorf("parse Herdr config %s for its settings: %w", path, err)
	}
	updated, added := string(raw), []string(nil)
	var failures []error
	leaves := herdrSettingsLeaves(nil, herdrSettingsEffective(installer.options.HerdrSettings))
	// A table's leaves are adjacent: each table's missing ones go in as one block, in key order; a
	// table that cannot take them fails alone, every other table's still added.
	for start := 0; start < len(leaves); {
		end := start + 1
		for end < len(leaves) && slices.Equal(leaves[end].table, leaves[start].table) {
			end++
		}
		table := leaves[start].table
		var block strings.Builder
		var names []string
		for _, setting := range leaves[start:end] {
			if herdrConfigHas(current, table, setting.key) {
				continue
			}
			line, err := renderHerdrSetting(setting.key, setting.value)
			if err != nil {
				return fmt.Errorf("render herdr setting %s: %w", herdrSettingName(table, setting.key), err)
			}
			value, err := renderHerdrSetting("v", setting.value)
			if err != nil {
				return fmt.Errorf("render herdr setting %s: %w", herdrSettingName(table, setting.key), err)
			}
			block.WriteString(line)
			names = append(
				names,
				herdrSettingName(table, setting.key)+" "+strings.TrimSpace(strings.TrimPrefix(value, "v")),
			)
		}
		start = end
		if block.Len() == 0 {
			continue
		}
		next, err := insertHerdrConfigLines(updated, table, block.String())
		if err != nil {
			failures = append(failures, installer.fail(fmt.Errorf("add herdr settings to %s: %w", path, err)))
			continue
		}
		updated, added = next, append(added, names...)
	}
	if len(added) == 0 {
		if len(failures) == 0 {
			installer.ok("herdr settings set in " + path)
		}
		return errors.Join(failures...)
	}
	return errors.Join(append(failures, installer.change(
		fmt.Sprintf("set herdr settings [%s] in %s", strings.Join(added, "; "), path),
		func() error { return installer.writeHerdrConfig(path, raw, []byte(updated), existed) },
	))...)
}

// herdrSettingName is key's dotted path, its table's segments first.
func herdrSettingName(table []string, key string) string {
	return strings.Join(append(slices.Clone(table), key), ".")
}

// herdrConfigHas reports whether config sets key in table; a table path
// holding a non-table value counts as set, the user's to keep.
func herdrConfigHas(config map[string]any, table []string, key string) bool {
	for _, segment := range table {
		value, present := config[segment]
		if !present {
			return false
		}
		inner, isTable := value.(map[string]any)
		if !isTable {
			return true
		}
		config = inner
	}
	_, present := config[key]
	return present
}

// renderHerdrSetting is key = value as one TOML line; a json.Number from
// herdr.settings renders an integer when it is one.
func renderHerdrSetting(key string, value any) (string, error) {
	var line bytes.Buffer
	if err := toml.NewEncoder(&line).Encode(map[string]any{key: herdrSettingTOML(value)}); err != nil {
		return "", err
	}
	return line.String(), nil
}

func herdrSettingTOML(value any) any {
	switch value := value.(type) {
	case json.Number:
		if integer, err := value.Int64(); err == nil {
			return integer
		}
		if float, err := value.Float64(); err == nil {
			return float
		}
		return value.String()
	case []any:
		items := make([]any, 0, len(value))
		for _, item := range value {
			items = append(items, herdrSettingTOML(item))
		}
		return items
	}
	return value
}
