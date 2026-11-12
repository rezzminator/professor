package claudelaunch

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
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

// SessionEnv returns the plugin values and the launch shell (ShellEnv) for a
// passthrough Claude session.
func SessionEnv(prefs pfmconfig.ClaudePrefs) []string {
	return append([]string{
		envFunctionHooks + "=1",
		envAutoCompactWindow + "=" + strconv.FormatInt(prefs.AutoCompactWindow, 10),
		envAnthropicSmallFastModel + "=" + helperModel(prefs),
	}, ShellEnv(paths.OSEnv{}.Lookup)...)
}
