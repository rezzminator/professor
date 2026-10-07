package run

import (
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// withLaunchShell adds the Claude launch shell (claudelaunch.ShellEnv) judged
// against environment itself, which may be the caller's explicit one: a usable
// inherited value stays, a refused one is replaced.
func withLaunchShell(environment []string) []string {
	lookup := func(name string) (string, bool) {
		for index := len(environment) - 1; index >= 0; index-- {
			if value, ok := strings.CutPrefix(environment[index], name+"="); ok {
				return value, true
			}
		}
		return "", false
	}
	for _, assignment := range claudelaunch.ShellEnv(lookup) {
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
