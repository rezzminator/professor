package config

import (
	"fmt"
	"slices"
	"strings"
)

func decodeClaudePrefs(raw rawClaude, path, scope string, index int) (ClaudePrefs, error) {
	prefs := ClaudePrefs{}
	if claudeEnvSet(raw.Settings) && index >= 0 {
		return ClaudePrefs{}, fmt.Errorf(
			"config %s: %s.settings is machine-wide: set %s", path, configScope(scope, index), KeyClaudeSettings,
		)
	}
	if raw.PermissionMode != nil {
		mode := *raw.PermissionMode
		// v1 used "prompt". Keep accepting it as an input alias while
		// materializing the v2 canonical value.
		if mode == "prompt" {
			mode = PermissionPrompt
		}
		if mode != PermissionBypass && mode != PermissionPrompt && mode != PermissionAuto {
			return ClaudePrefs{}, fmt.Errorf(
				"config %s: %s.permissionMode must be %q, %q or %q, got %q",
				path,
				configScope(scope, index),
				PermissionBypass,
				PermissionPrompt,
				PermissionAuto,
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
	if raw.PluginSource != nil && index >= 0 {
		return ClaudePrefs{}, fmt.Errorf(
			"config %s: %s.pluginSource is machine-wide: set %s",
			path,
			configScope(scope, index),
			KeyClaudePluginSource,
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
	if claudeEnvSet(raw.Env) && index >= 0 {
		return ClaudePrefs{}, fmt.Errorf(
			"config %s: %s.env is machine-wide: set %s",
			path,
			configScope(scope, index),
			KeyClaudeEnv,
		)
	}
	if raw.GptAgents != nil && index >= 0 {
		return ClaudePrefs{}, fmt.Errorf(
			"config %s: %s.gptAgents is machine-wide: set %s",
			path,
			configScope(scope, index),
			KeyClaudeGptAgents,
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

// copyClaudeLaunchPrefs sets the launch values an account block carries or
// inherits whole: Load seeds an account's from the resolved top level, and
// EffectiveClaude reads them back from the account.
func copyClaudeLaunchPrefs(target *ClaudePrefs, source ClaudePrefs) {
	target.RequireManagedCleanup = source.RequireManagedCleanup
}

// applyClaudeMachinePrefs sets the top-level claude keys no account may carry
// (decodeClaudePrefs refuses them there): the plugin checkout root, the plugin
// source, the plugin list, the env map and the cost ledger switch.
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
	if raw.PluginSource != nil {
		source := *raw.PluginSource
		if source != PluginSourceRelease && source != PluginSourceCheckout {
			return fmt.Errorf("config %s: %s must be %q or %q, got %q",
				path, KeyClaudePluginSource, PluginSourceRelease, PluginSourceCheckout, source)
		}
		target.PluginSource = source
		sources[KeyClaudePluginSource] = SourceFile
	}
	if raw.Plugins != nil {
		plugins, err := decodeClaudePlugins(*raw.Plugins)
		if err != nil {
			return fmt.Errorf("config %s: %w", path, err)
		}
		target.Plugins = plugins
		sources[KeyClaudePlugins] = SourceFile
	}
	if claudeEnvSet(raw.Env) {
		env, err := DecodeEnv(KeyClaudeEnv, raw.Env)
		if err != nil {
			return fmt.Errorf("config %s: %w", path, err)
		}
		target.Env = env
		sources[KeyClaudeEnv] = SourceFile
	}
	if raw.GptAgents != nil {
		target.GptAgents = *raw.GptAgents
		sources[KeyClaudeGptAgents] = SourceFile
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
	if raw.RequireManagedCleanup != nil {
		target.RequireManagedCleanup = *raw.RequireManagedCleanup
		sources[base+"requireManagedCleanup"] = SourceFile
	}
}

// PluginsFromCheckout reports whether Professor's own plugins come from their
// local checkouts (claude.pluginSource "checkout") rather than the GitHub
// professor marketplace: the one door every plugin-source decision reads,
// install and doctor alike, whatever pfm's own build version.
func (prefs ClaudePrefs) PluginsFromCheckout() bool {
	return prefs.PluginSource == PluginSourceCheckout
}

// marshalClaude is the machine claude block. The keys a pfm of the last release (v0.79.0) does
// not know — pluginCheckoutRoot, pluginSource, plugins, env, costLedger — are written only when the file
// set them or the value differs from the default, so `pfm config init` writes a file that pfm still
// decodes: its loader refuses an unknown key. An unset value loads as its default either way.
func marshalClaude(config Config) map[string]any {
	value := map[string]any{
		"requireManagedCleanup": config.Claude.RequireManagedCleanup,
		"permissionMode":        config.Claude.PermissionMode,
		engineKeyBinary:         config.Claude.Binary,
		keyTheme:                themeMarshalValue(config.Claude.Theme),
		"cache1h":               config.Claude.Cache1H,
	}
	set := func(key string) bool { return config.Source(key) == SourceFile }
	if set("claude.remoteControl") || !config.Claude.RemoteControl {
		value["remoteControl"] = config.Claude.RemoteControl
	}
	if config.Claude.PluginCheckoutRoot != "" { // an unset root is left out: null would also be an unknown key
		value["pluginCheckoutRoot"] = config.Claude.PluginCheckoutRoot
	}
	if set(KeyClaudePluginSource) || config.Claude.PluginsFromCheckout() {
		value["pluginSource"] = config.Claude.PluginSource
	}
	if set(KeyClaudePlugins) || !slices.Equal(config.Claude.Plugins, DefaultClaudePlugins()) {
		value["plugins"] = marshalClaudePlugins(config.Claude.Plugins)
	}
	if set(KeyClaudeEnv) || len(config.Claude.Env) != 0 {
		env := make(map[string]any, len(config.Claude.Env))
		for name, value := range config.Claude.Env {
			env[name] = value
		}
		value["env"] = env
	}
	if set(KeyClaudeCostLedger) || !config.Claude.CostLedger {
		value["costLedger"] = config.Claude.CostLedger
	}
	if set(KeyClaudeGptAgents) || config.Claude.GptAgents {
		value["gptAgents"] = config.Claude.GptAgents
	}
	return value
}

// marshalClaudeAccount preserves explicit account switches, including false overrides.
func marshalClaudeAccount(config Config, account Account, index int) map[string]any {
	claude := map[string]any{
		"requireManagedCleanup": account.Claude.RequireManagedCleanup,
		keyTheme:                themeMarshalValue(account.Claude.Theme),
		"cache1h":               account.Claude.Cache1H,
	}
	if config.Source(fmt.Sprintf("accounts[%d].claude.remoteControl", index)) == SourceFile ||
		account.Claude.RemoteControl != config.Claude.RemoteControl {
		claude["remoteControl"] = account.Claude.RemoteControl
	}
	// These empty sentinels mean inherit. Writing them as explicit empty
	// values makes our strict loader reject its own output.
	if account.Claude.PermissionMode != "" {
		claude["permissionMode"] = account.Claude.PermissionMode
	}
	if account.Claude.Binary != "" {
		claude[engineKeyBinary] = account.Claude.Binary
	}
	return claude
}
