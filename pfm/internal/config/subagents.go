package config

import (
	"fmt"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// Claude Code lets a sub-agent spawn sub-agents only 3 levels deep and runs at
// most 20 of them at once. A fleet that orchestrates through nested agents
// hits both ceilings, and the harness reports neither — the spawn simply does
// not happen. Two integers under the claude block lift them, and the launch
// door turns them into the environment variables the harness reads.
const (
	// DefaultSubagentSpawnDepth is the fleet's depth when the file names
	// none. Unlike the concurrency cap it is always sent: the harness's own 3
	// is too shallow for the fleet's agents-spawning-agents cast, so "no key"
	// must still mean a lifted depth, not the harness default.
	DefaultSubagentSpawnDepth = 8

	spawnDepthKey  = "maxSubagentSpawnDepth"
	concurrencyKey = "maxConcurrentSubagents"
)

// applySubagentCaps resolves both caps for one scope over inherited — the
// pre-file defaults for the top-level claude block (index -1), the resolved
// top-level values for accounts[index].claude. Zero is the unset sentinel on
// both fields, so a scope that names neither key keeps what it inherited and
// EffectiveClaude can copy a set value unconditionally.
func applySubagentCaps(
	prefs *ClaudePrefs,
	raw rawClaude,
	inherited ClaudePrefs,
	path, scope string,
	index int,
	sources map[string]Source,
) error {
	depth, err := subagentCap(raw.MaxSubagentSpawnDepth, spawnDepthKey, path, scope, index)
	if err != nil {
		return err
	}
	concurrency, err := subagentCap(raw.MaxConcurrentSubagents, concurrencyKey, path, scope, index)
	if err != nil {
		return err
	}
	prefs.MaxSubagentSpawnDepth = inherited.MaxSubagentSpawnDepth
	prefs.MaxConcurrentSubagents = inherited.MaxConcurrentSubagents
	if depth != 0 {
		prefs.MaxSubagentSpawnDepth = depth
		sources[subagentSourceKey(spawnDepthKey, index)] = SourceFile
	}
	if concurrency != 0 {
		prefs.MaxConcurrentSubagents = concurrency
		sources[subagentSourceKey(concurrencyKey, index)] = SourceFile
	}
	return nil
}

// subagentCap validates one cap at data entry. A non-integer never reaches
// here — the strict decoder rejects it and names the field — so the only
// invalid value left is a number Claude Code cannot act on.
func subagentCap(raw *int, key, path, scope string, index int) (int, error) {
	if raw == nil {
		return 0, nil
	}
	if *raw < 1 {
		return 0, fmt.Errorf(
			"config %s: %s.%s must be a positive integer, got %d",
			path,
			configScope(scope, index),
			key,
			*raw,
		)
	}
	return *raw, nil
}

func subagentSourceKey(key string, index int) string {
	if index >= 0 {
		return fmt.Sprintf("accounts[%d].claude.%s", index, key)
	}
	return engineConfigKey(pfmengine.Claude, key)
}
