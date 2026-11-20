package config

import (
	"maps"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Reload is runtime with its machine config read again from the file it was
// loaded from, its paths kept. A long-lived process (the MCP servers) outlives
// config edits while the CLI reads the file at every launch, so it reloads
// before each chat verb it dispatches: an edit reaches the next chat it starts,
// and a broken edit is the error, never a launch on the copy read at start. A
// runtime loaded from no file is returned as it is. The Codex homes default
// to the ambient one (paths.CodexHome), as at the first load: the runtime's
// own Codex roots are the file's homes as read then, so an edit dropping
// codex.homes would otherwise keep the old first home.
func (runtime Runtime) Reload() (Runtime, error) {
	if runtime.Config.Path == "" {
		return runtime, nil
	}
	machine, err := Load(
		runtime.Config.Path,
		runtime.Paths.Home,
		runtime.Paths.Roots[pfmengine.Claude],
		paths.CodexHome(runtime.Paths.Home),
	)
	if err != nil {
		return runtime, err
	}
	runtime.Config = machine
	if runtime.Paths.Roots != nil {
		// The copy gets its own map: the shared MCP daemon reloads on every
		// verb, concurrently, from the one runtime other verbs are reading.
		runtime.Paths.Roots = maps.Clone(runtime.Paths.Roots)
		runtime.Paths.Roots[pfmengine.Codex] = machine.CodexHomes()
	}
	return runtime, nil
}
