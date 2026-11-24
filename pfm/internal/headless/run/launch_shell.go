package run

import (
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// withLaunchShell adds the Claude launch shell and PATH (claudelaunch.ShellEnv)
// judged against environment itself, which may be the caller's explicit one: a
// usable inherited shell stays, a refused one is replaced, and a PATH the login
// shell's adds to replaces the inherited one.
func withLaunchShell(environment []string) []string {
	return withLaunchShellThrough(environment, nil)
}

// withLaunchShellThrough is withLaunchShell resolving bash and running the login shell through runner.
func withLaunchShellThrough(environment []string, runner deps.Runner) []string {
	lookup := func(name string) (string, bool) {
		for index := len(environment) - 1; index >= 0; index-- {
			if value, ok := strings.CutPrefix(environment[index], name+"="); ok {
				return value, true
			}
		}
		return "", false
	}
	for _, assignment := range claudelaunch.ShellEnvThrough(lookup, runner) {
		name, _, _ := strings.Cut(assignment, "=")
		kept := environment[:0:0]
		for _, entry := range environment {
			if !strings.HasPrefix(entry, name+"=") {
				kept = append(kept, entry)
			}
		}
		kept = append(kept, assignment)
		environment = kept
	}
	return environment
}

func checkCodexRunLogin(request Request) error {
	if request.Engine == pfmengine.Codex && !request.WithoutAccount && request.ConfigDir != "" {
		return pfmconfig.CodexLoginError(request.ConfigDir)
	}
	return nil
}
