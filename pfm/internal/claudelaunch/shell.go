package claudelaunch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
// here and replaced by bash.
func resolveShell(lookup func(string) (string, bool)) launchShell {
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
	path, err := deps.Resolve(shellBash)
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

// ShellEnv is the CLAUDE_CODE_SHELL assignment a Claude launch adds to the
// environment lookup reads, so the Bash tool of the chat and, through
// inheritance, of its sub-agents runs under bash rather than the user's login
// shell: zsh does not word-split an unquoted $VAR the way the bash idioms
// agents write expect. A usable inherited value yields no assignment and is
// kept; every refused value and a missing bash is warned to the activity log
// and shown by `pfm config claude`, and never fails the launch.
func ShellEnv(lookup func(string) (string, bool)) []string {
	resolved := resolveShell(lookup)
	for _, problem := range resolved.problems {
		obs.Logger(context.Background()).Warn("claudelaunch: launch shell", obs.FieldErr, problem)
	}
	if resolved.path == "" || resolved.preset {
		return nil
	}
	return []string{envShell + "=" + resolved.path}
}

// shellValue is the `pfm config` view of the shell knob: the shell a launch
// from this process would run, and where it came from; with no shell, the
// reason, never an "unset" that reads as a settled answer.
func shellValue() (value, won string) {
	resolved := resolveShell(paths.OSEnv{}.Lookup)
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
