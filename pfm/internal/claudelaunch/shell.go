package claudelaunch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	knobShell = "shell"
	envShell  = "CLAUDE_CODE_SHELL"
	shellBash = "bash"
	shellZsh  = "zsh"
	wonEnv    = "env"
	wonPATH   = "PATH"
)

// launchShell is the shell Claude Code's Bash tool runs under for one launch:
// path is "" when none was found, preset marks an inherited value kept as it
// is, problems carries every refused value and a missing bash.
type launchShell struct {
	path     string
	preset   bool
	problems []string
}

// resolveShell keeps a usable CLAUDE_CODE_SHELL the launch environment already
// carries; otherwise it resolves bash on PATH. Claude Code honours the variable
// only for an executable whose path names bash or zsh and silently falls back
// to the login shell for anything else, so a value it would refuse is reported
// here and replaced by bash. runner resolves bash; nil is the real one.
func resolveShell(lookup func(string) (string, bool), runner deps.Runner) launchShell {
	var result launchShell
	if value, ok := lookup(envShell); ok {
		problem := shellProblem(value)
		if problem == "" {
			return launchShell{path: value, preset: true}
		}
		result.problems = append(
			result.problems,
			fmt.Sprintf("%s=%q %s; resolving bash instead", envShell, value, problem),
		)
	}
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	path, err := runner.LookPath(shellBash)
	if err != nil {
		result.problems = append(result.problems, fmt.Sprintf(
			"no bash on PATH (%v): %s left unset, Claude Code's Bash tool runs under the login shell",
			err, envShell,
		))
		return result
	}
	result.path = path
	return result
}

// shellProblem is why Claude Code would refuse value as its shell, or "".
func shellProblem(value string) string {
	if !filepath.IsAbs(value) {
		return "is not an absolute path"
	}
	if !strings.Contains(value, shellBash) && !strings.Contains(value, shellZsh) {
		return "names neither bash nor zsh"
	}
	info, err := os.Stat(value)
	if err != nil {
		return fmt.Sprintf("cannot be read: %v", err)
	}
	if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return "is not an executable file"
	}
	return ""
}

// ShellEnv is the CLAUDE_CODE_SHELL and PATH assignments a Claude launch adds
// to the environment lookup reads: the shell so the Bash tool of the chat and, through
// inheritance, of its sub-agents runs under bash rather than the user's login
// shell: zsh does not word-split an unquoted $VAR the way the bash idioms
// agents write expect. A usable inherited value yields no assignment and is
// kept; every refused value and a missing bash is warned to the activity log
// and shown by `pfm config claude`, and never fails the launch. The PATH is
// the launch's own with the login shell's missing entries merged in
// (pathEnvThrough), since that bash reads no startup file.
func ShellEnv(lookup func(string) (string, bool)) []string {
	return ShellEnvThrough(lookup, nil)
}

// ShellEnvThrough is ShellEnv resolving bash and running the login shell
// through runner; nil is the real one.
func ShellEnvThrough(lookup func(string) (string, bool), runner deps.Runner) []string {
	env, _ := launchShellEnv(lookup, runner)
	return env
}

// launchShellEnv is ShellEnvThrough, with the login entries its PATH adds (pathEnvThrough).
func launchShellEnv(lookup func(string) (string, bool), runner deps.Runner) (env, loginAdded []string) {
	resolved := resolveShell(lookup, runner)
	for _, problem := range resolved.problems {
		obs.Logger(context.Background()).Warn("claudelaunch: launch shell", obs.FieldErr, problem)
	}
	path, loginAdded := pathEnvThrough(lookup, clock.Real, runner)
	if resolved.path == "" || resolved.preset {
		return path, loginAdded
	}
	return append([]string{envShell + "=" + resolved.path}, path...), loginAdded
}

// requestShellEnv is the shell and PATH assignments of request's launch, a
// persona's env the base they build on (its value read first), and the login
// entries its PATH adds when no persona PATH is that base (Launch.LoginPATH).
func requestShellEnv(request Request) (env, loginPATH []string) {
	launchEnv := func(name string) (string, bool) {
		if value, ok := request.Envs[name]; ok {
			return value, true
		}
		return paths.OSEnv{}.Lookup(name)
	}
	env, loginPATH = launchShellEnv(launchEnv, request.Runner)
	if _, persona := request.Envs[envPATH]; persona {
		return env, nil
	}
	return env, loginPATH
}

// shellValue is the `pfm config` view of the shell knob: the shell a launch
// from this process would run, and where it came from; with no shell, the
// reason, never an "unset" that reads as a settled answer.
func shellValue() (value, won string) {
	resolved := resolveShell(paths.OSEnv{}.Lookup, nil)
	won = wonPATH
	if resolved.preset {
		won = wonEnv
	}
	parts := resolved.problems
	if resolved.path != "" {
		parts = append([]string{resolved.path}, parts...)
	}
	return strings.Join(parts, "; "), won
}
