package main

import (
	"hostops/pfm/internal/action"
	"hostops/pfm/internal/ask"
	pfmengine "hostops/pfm/internal/engine"
	claudeengine "hostops/pfm/internal/engine/claude"
	codexengine "hostops/pfm/internal/engine/codex"
	opencodeengine "hostops/pfm/internal/engine/opencode"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/index"
	"hostops/pfm/internal/spawn"
	"hostops/pfm/internal/stats"
)

// registerEngines is the single composition root for engine capabilities.
func registerEngines() {
	index.RegisterSource(pfmengine.Claude, claudeengine.Source{})
	index.RegisterSource(pfmengine.Codex, codexengine.Source{})
	index.RegisterSource(pfmengine.OpenCode, opencodeengine.Source{})

	spawn.RegisterLauncher(pfmengine.Claude, claudeengine.Launcher{})
	spawn.RegisterLauncher(pfmengine.Codex, codexengine.Launcher{})

	gather.RegisterMatcher(pfmengine.Claude, claudeengine.Matcher{})
	gather.RegisterMatcher(pfmengine.Codex, codexengine.Matcher{})
	gather.RegisterMatcher(pfmengine.OpenCode, opencodeengine.Matcher{})

	stats.RegisterUsageSource(pfmengine.Claude, claudeengine.UsageSource{})
	stats.RegisterUsageSource(pfmengine.Codex, codexengine.UsageSource{})

	action.RegisterPlanner(pfmengine.Claude, claudeengine.HeadlessPlanner{})
	action.RegisterPlanner(pfmengine.Codex, codexengine.HeadlessPlanner{})

	ask.RegisterRunner(pfmengine.Claude, claudeengine.AskRunner{})
	ask.RegisterRunner(pfmengine.Codex, codexengine.AskRunner{})
}

func init() { registerEngines() }

var engineCapabilityExceptions = map[pfmengine.ID]map[string]bool{
	// OpenCode has a session index and a process matcher. It has no usage API,
	// headless planner, ask runner, or managed launcher in this tree.
	pfmengine.OpenCode: {indexCommand: true, "matcher": true},
}

func expectedEngineCapabilities(id pfmengine.ID, all []string) map[string]bool {
	if expected, ok := engineCapabilityExceptions[id]; ok {
		return expected
	}
	result := make(map[string]bool, len(all))
	for _, capability := range all {
		result[capability] = true
	}
	return result
}
