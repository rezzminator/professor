package action

import (
	"sort"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// ExtensionShellEnv renders configured assignments of active extensions only.
// Inactive switches and every unconfigured inherited name remain untouched.
func ExtensionShellEnv(machine pfmconfig.Config, engine string) string {
	var result strings.Builder
	env := machine.ExtensionEnv(engine)
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		result.WriteString(" " + name + "=" + Quote(env[name]))
	}
	return result.String()
}
