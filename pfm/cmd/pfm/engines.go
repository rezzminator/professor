package main

import (
	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/ask"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	claudeengine "github.com/rezzminator/professor/pfm/internal/engine/claude"
	codexengine "github.com/rezzminator/professor/pfm/internal/engine/codex"
	opencodeengine "github.com/rezzminator/professor/pfm/internal/engine/opencode"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/stats"
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
