package config

import (
	"fmt"
	"strings"
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
