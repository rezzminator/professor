package claudelaunch

import (
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/deps"
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

// PaneEnv is Env as the command a pane shell runs carries it, and the PATH word
// that follows it, space-led: with LoginPATH set, Env's PATH (pfm's own merged)
// is left out and the word adds only those folders after the pane shell's own
// "$PATH", so the pane's order stays first; else Env whole and no word. The word
// is POSIX shell, read by sh, bash and zsh alike (fish joins a quoted path
// variable with colons too).
func (launch Launch) PaneEnv() (env []string, pathWord string) {
	if launch.LoginPATH == nil {
		return launch.Env, ""
	}
	for _, assignment := range launch.Env {
		if !strings.HasPrefix(assignment, envPATH+"=") {
			env = append(env, assignment)
		}
	}
	separator := string(os.PathListSeparator)
	added := cmdparse.Quote(separator + strings.Join(launch.LoginPATH, separator))
	return env, ` ` + envPATH + `="$` + envPATH + `"` + added
}

// sessionEnvNames are the variables a passthrough Claude session carries in its process
// environment, the plugins reading them there.
var sessionEnvNames = []string{envFunctionHooks, envAutoCompactWindow, envAnthropicSmallFastModel}

// SessionEnv returns the plugin values, each its effective value under claude (the claude.settings
// laid over the shipped defaults, EffectiveSettings), and the launch shell and PATH (ShellEnv) for a
// passthrough Claude session. A variable claude sets to null is left out.
func SessionEnv(claude map[string]any) []string {
	return sessionEnvThrough(claude, paths.OSEnv{}.Lookup, nil)
}

// sessionEnvThrough is SessionEnv over the environment lookup reads, the shell and PATH through runner.
func sessionEnvThrough(claude map[string]any, lookup func(string) (string, bool), runner deps.Runner) []string {
	env := EffectiveEnv(claude)
	result := make([]string, 0, len(sessionEnvNames))
	for _, name := range sessionEnvNames {
		if value, set := env[name]; set {
			result = append(result, name+"="+value)
		}
	}
	return append(result, ShellEnvThrough(lookup, runner)...)
}
