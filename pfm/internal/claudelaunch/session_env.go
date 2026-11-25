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
// launch.Unset and every inherited name launch.Env assigns dropped, then
// launch.Env (paths.ApplyEnv).
func RunEnvironment(environ []string, launch Launch) []string {
	return paths.ApplyEnv(WithoutCodexToolShell(environ), launch.Unset, launch.Env)
}

// recipeKept names what a seat recipe carries from its writer's environment
// though the launch assigns none: PATH, the search path every engine's tools
// resolve on, which the Herdr server a new-tab seat host inherits may lack;
// CLAUDE_CODE_SHELL, a usable preset resolveShell keeps instead of assigning.
var recipeKept = []string{envPATH, envShell}

// Recipe is launch as a seat recipe stores it (fleetdb.SeatRun with EnvDelta),
// which the seat host applies over its own environment: env is each
// recipeKept name environ holds non-empty that launch neither assigns nor unsets, then
// launch.Env; unset is launch.Unset. Nothing else of environ rides, so a
// variable the writing chat's settings, a Codex tool shell or an older config
// put there never reaches the engine; a Codex tool shell the seat host itself
// runs in is the seat host's to strip (WithoutCodexToolShell).
func (launch Launch) Recipe(environ []string) (env, unset []string) {
	owned := make(map[string]bool, len(launch.Unset)+len(launch.Env))
	for _, name := range launch.Unset {
		owned[name] = true
	}
	for _, assignment := range launch.Env {
		name, _, _ := strings.Cut(assignment, "=")
		owned[name] = true
	}
	env = []string{}
	for _, name := range recipeKept {
		if owned[name] {
			continue
		}
		for _, entry := range environ {
			if value, found := strings.CutPrefix(entry, name+"="); found {
				if value != "" {
					env = append(env, name+"="+value)
				}
				break
			}
		}
	}
	env = append(env, launch.Env...)
	return env, append([]string{}, launch.Unset...)
}

// paneLoginPATHScript is the /bin/sh program PaneEnv's words run: it adds its
// first argument after the PATH it inherits, with no empty entry when that PATH is
// empty or unset (an empty entry searches the working directory), and execs the rest.
const paneLoginPATHScript = `PATH="${PATH:+$PATH:}$1"; export PATH; shift; exec "$@"`

// PaneEnv is Env as the command a pane shell runs carries it, and the words that
// follow it, space-led: with LoginPATH set, Env's PATH (pfm's own merged) is left
// out and the words run paneLoginPATHScript in /bin/sh over those folders, so the
// pane shell's own PATH stays first; else Env whole and no words. The words are
// literals in single quotes, read alike by every shell tmux can run a pane
// command in (its default-shell: sh, bash, zsh, fish), so no shell's expansion
// rules decide the PATH.
func (launch Launch) PaneEnv() (env []string, pathWords string) {
	if launch.LoginPATH == nil {
		return launch.Env, ""
	}
	for _, assignment := range launch.Env {
		if !strings.HasPrefix(assignment, envPATH+"=") {
			env = append(env, assignment)
		}
	}
	folders := strings.Join(launch.LoginPATH, string(os.PathListSeparator))
	return env, ` /bin/sh -c ` + cmdparse.Quote(paneLoginPATHScript) + ` sh ` + cmdparse.Quote(folders)
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
