package claudelaunch

import (
	"maps"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// WithEnvs is env, a launch's own assignments, behind the adopter settings'
// envs in name order: a name env assigns stays pfm's.
func WithEnvs(envs map[string]string, env []string) []string {
	if len(envs) == 0 {
		return env
	}
	owned := make(map[string]bool, len(env))
	for _, assignment := range env {
		name, _, _ := strings.Cut(assignment, "=")
		owned[name] = true
	}
	result := make([]string, 0, len(envs)+len(env))
	for _, name := range slices.Sorted(maps.Keys(envs)) {
		if !owned[name] {
			result = append(result, name+"="+envs[name])
		}
	}
	return append(result, env...)
}

// RunEnvironment is environ as a launch runs it: a Codex tool shell's markers,
// launch.Unset and every inherited name launch.Env assigns dropped (an exec'd
// process reads the first of two copies), then launch.Env.
func RunEnvironment(environ []string, launch Launch) []string {
	dropped := make(map[string]bool, len(launch.Unset)+len(launch.Env))
	for _, name := range launch.Unset {
		dropped[name] = true
	}
	for _, assignment := range launch.Env {
		name, _, _ := strings.Cut(assignment, "=")
		dropped[name] = true
	}
	result := make([]string, 0, len(environ)+len(launch.Env))
	for _, entry := range WithoutCodexToolShell(environ) {
		name, _, _ := strings.Cut(entry, "=")
		if !dropped[name] {
			result = append(result, entry)
		}
	}
	return append(result, launch.Env...)
}

// sessionEnvNames are the variables a passthrough Claude session carries in its process
// environment, the plugins reading them there.
var sessionEnvNames = []string{envFunctionHooks, envAutoCompactWindow, envAnthropicSmallFastModel}

// SessionEnv returns the plugin values, each its effective value under claude (the claude.settings
// laid over the shipped defaults, EffectiveSettings), and the launch shell and PATH (ShellEnv) for a
// passthrough Claude session. A variable claude sets to null is left out.
func SessionEnv(claude map[string]any) []string {
	env := EffectiveEnv(claude)
	result := make([]string, 0, len(sessionEnvNames))
	for _, name := range sessionEnvNames {
		if value, set := env[name]; set {
			result = append(result, name+"="+value)
		}
	}
	return append(result, ShellEnv(paths.OSEnv{}.Lookup)...)
}
