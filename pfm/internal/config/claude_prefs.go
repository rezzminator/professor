package config

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

func decodeClaudePrefs(raw rawClaude, path, scope string, index int) (ClaudePrefs, error) {
	prefs := ClaudePrefs{}
	if raw.WebSearchesPerSession != nil && *raw.WebSearchesPerSession < 1 {
		return ClaudePrefs{}, fmt.Errorf(
			"config %s: %s.webSearchesPerSession must be at least 1", path, configScope(scope, index),
		)
	}
	if raw.AutoCompactWindow != nil && *raw.AutoCompactWindow < 1 {
		return ClaudePrefs{}, fmt.Errorf(
			"config %s: %s.autoCompactWindow must be at least 1", path, configScope(scope, index),
		)
	}
	if raw.HelperModel != nil && !ValidHelperModel(*raw.HelperModel) {
		return ClaudePrefs{}, fmt.Errorf(
			"config %s: %s.helperModel must be a non-empty model name without whitespace",
			path,
			configScope(scope, index),
		)
	}
	if raw.CleanupPeriodDays != nil && *raw.CleanupPeriodDays < 1 {
		return ClaudePrefs{}, fmt.Errorf(
			"config %s: %s.cleanupPeriodDays must be at least 1", path, configScope(scope, index),
		)
	}
	if raw.PermissionMode != nil {
		mode := *raw.PermissionMode
		// v1 used "prompt". Keep accepting it as an input alias while
		// materializing the v2 canonical value.
		if mode == "prompt" {
			mode = PermissionPrompt
		}
		if mode != PermissionBypass && mode != PermissionPrompt {
			return ClaudePrefs{}, fmt.Errorf(
				"config %s: %s.permissionMode must be %q or %q, got %q",
				path,
				configScope(scope, index),
				PermissionBypass,
				PermissionPrompt,
				*raw.PermissionMode,
			)
		}
		prefs.PermissionMode = mode
	}
	if raw.Binary != nil {
		if strings.TrimSpace(*raw.Binary) == "" || strings.ContainsRune(*raw.Binary, '\x00') {
			return ClaudePrefs{}, fmt.Errorf(
				"config %s: %s.binary must be a non-empty command",
				path,
				configScope(scope, index),
			)
		}
		prefs.Binary = *raw.Binary
	}
	if raw.PluginCheckoutRoot != nil && index >= 0 {
		return ClaudePrefs{}, fmt.Errorf(
			"config %s: %s.pluginCheckoutRoot is machine-wide: set %s",
			path,
			configScope(scope, index),
			KeyClaudePluginCheckoutRoot,
		)
	}
	if raw.Plugins != nil && index >= 0 {
		return ClaudePrefs{}, fmt.Errorf(
			"config %s: %s.plugins is machine-wide: set %s",
			path,
			configScope(scope, index),
			KeyClaudePlugins,
		)
	}
	if raw.CostLedger != nil && index >= 0 {
		return ClaudePrefs{}, fmt.Errorf(
			"config %s: %s.costLedger is machine-wide: set %s",
			path,
			configScope(scope, index),
			KeyClaudeCostLedger,
		)
	}
	if raw.Cache1H != nil {
		prefs.Cache1H = *raw.Cache1H
	}
	if raw.SystemPrompt != nil {
		value := *raw.SystemPrompt
		if value != SystemPromptProduction && value != SystemPromptLean && value != SystemPromptProfessor {
			return ClaudePrefs{}, fmt.Errorf(
				"config %s: %s.systemPrompt must be %q, %q or %q, got %q",
				path,
				configScope(scope, index),
				SystemPromptProduction,
				SystemPromptLean,
				SystemPromptProfessor,
				value,
			)
		}
		prefs.SystemPrompt = value
	}
	return prefs, nil
}

// ValidHelperModel reports whether value can be a helper model: a model id or
// alias, non-empty, without a space or control character, so it reaches Claude
// Code's environment as one word.
func ValidHelperModel(value string) bool {
	return value != "" && !strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})
}

// copyClaudeLaunchPrefs sets the launch values an account block carries or
// inherits whole: Load seeds an account's from the resolved top level, and
// EffectiveClaude reads them back from the account.
func copyClaudeLaunchPrefs(target *ClaudePrefs, source ClaudePrefs) {
	target.WebSearchesPerSession = source.WebSearchesPerSession
	target.AutoCompactWindow = source.AutoCompactWindow
	target.HelperModel = source.HelperModel
	target.TmuxTruecolor = source.TmuxTruecolor
	target.CleanupPeriodDays = source.CleanupPeriodDays
	target.RequireManagedCleanup = source.RequireManagedCleanup
}

// applyClaudeMachinePrefs sets the top-level claude keys no account may carry
// (decodeClaudePrefs refuses them there): the plugin checkout root, the plugin
// list and the cost ledger switch.
func applyClaudeMachinePrefs(
	target *ClaudePrefs, raw rawClaude, home, path string, sources map[string]Source,
) error {
	if raw.PluginCheckoutRoot != nil {
		root, err := expandHomePath(*raw.PluginCheckoutRoot, home)
		if err != nil {
			return fmt.Errorf("config %s: %s %w", path, KeyClaudePluginCheckoutRoot, err)
		}
		target.PluginCheckoutRoot = root
		sources[KeyClaudePluginCheckoutRoot] = SourceFile
	}
	if raw.Plugins != nil {
		plugins, err := decodeClaudePlugins(*raw.Plugins)
		if err != nil {
			return fmt.Errorf("config %s: %w", path, err)
		}
		target.Plugins = plugins
		sources[KeyClaudePlugins] = SourceFile
	}
	if raw.CostLedger != nil {
		target.CostLedger = *raw.CostLedger
		sources[KeyClaudeCostLedger] = SourceFile
	}
	return nil
}

func applyClaudeLaunchPrefs(target *ClaudePrefs, raw rawClaude, sources map[string]Source, scope string, index int) {
	base := scope + "."
	if index >= 0 {
		base = fmt.Sprintf("accounts[%d].claude.", index)
	}
	if raw.WebSearchesPerSession != nil {
		target.WebSearchesPerSession = *raw.WebSearchesPerSession
		sources[base+"webSearchesPerSession"] = SourceFile
	}
	if raw.AutoCompactWindow != nil {
		target.AutoCompactWindow = *raw.AutoCompactWindow
		sources[base+"autoCompactWindow"] = SourceFile
	}
	if raw.HelperModel != nil {
		target.HelperModel = *raw.HelperModel
		sources[base+"helperModel"] = SourceFile
	}
	if raw.TmuxTruecolor != nil {
		target.TmuxTruecolor = *raw.TmuxTruecolor
		sources[base+"tmuxTruecolor"] = SourceFile
	}
	if raw.CleanupPeriodDays != nil {
		target.CleanupPeriodDays = *raw.CleanupPeriodDays
		sources[base+"cleanupPeriodDays"] = SourceFile
	}
	if raw.RequireManagedCleanup != nil {
		target.RequireManagedCleanup = *raw.RequireManagedCleanup
		sources[base+"requireManagedCleanup"] = SourceFile
	}
}

// marshalClaude is the machine claude block. The keys a pfm of the last release (v0.79.0) does
// not know — helperModel, pluginCheckoutRoot, plugins, costLedger — are written only when the file
// set them or the value differs from the default, so `pfm config init` writes a file that pfm still
// decodes: its loader refuses an unknown key. An unset value loads as its default either way.
func marshalClaude(config Config) map[string]any {
	value := map[string]any{
		"webSearchesPerSession": config.Claude.WebSearchesPerSession,
		"autoCompactWindow":     config.Claude.AutoCompactWindow,
		"tmuxTruecolor":         config.Claude.TmuxTruecolor,
		"cleanupPeriodDays":     config.Claude.CleanupPeriodDays,
		"requireManagedCleanup": config.Claude.RequireManagedCleanup,
		"permissionMode":        config.Claude.PermissionMode,
		engineKeyBinary:         config.Claude.Binary,
		keyTheme:                themeMarshalValue(config.Claude.Theme),
		"cache1h":               config.Claude.Cache1H,
		"nativeCursor":          config.Claude.NativeCursor,
	}
	set := func(key string) bool { return config.Source(key) == SourceFile }
	if set("claude.helperModel") || config.Claude.HelperModel != DefaultHelperModel {
		value["helperModel"] = config.Claude.HelperModel
	}
	if config.Claude.PluginCheckoutRoot != "" { // an unset root is left out: null would also be an unknown key
		value["pluginCheckoutRoot"] = config.Claude.PluginCheckoutRoot
	}
	if set(KeyClaudePlugins) || !slices.Equal(config.Claude.Plugins, DefaultClaudePlugins()) {
		value["plugins"] = marshalClaudePlugins(config.Claude.Plugins)
	}
	if set(KeyClaudeCostLedger) || !config.Claude.CostLedger {
		value["costLedger"] = config.Claude.CostLedger
	}
	return value
}
