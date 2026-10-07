package deps

import (
	"os"
	"strings"
)

// EnvironmentWith returns the process environment with key set exactly once.
func EnvironmentWith(key, value string) []string {
	prefix := key + "="
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, prefix+value)
}

// gitRepoVars select a repository or its storage: a git call drops them,
// so no inherited GIT_DIR or GIT_WORK_TREE steers it into another repository,
// while every other GIT_* (a CA bundle, a proxy, an ssh command) passes through.
var gitRepoVars = map[string]bool{
	"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_COMMON_DIR": true, "GIT_INDEX_FILE": true,
	"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true, "GIT_NAMESPACE": true,
	"GIT_IMPLICIT_WORK_TREE": true, "GIT_PREFIX": true, "GIT_SHALLOW_FILE": true, "GIT_GRAFT_FILE": true,
	"GIT_CEILING_DIRECTORIES": true,
}

// WithoutGitRepoVars returns a new environment without repository selectors.
func WithoutGitRepoVars(environ []string) []string {
	environment := make([]string, 0, len(environ))
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if !gitRepoVars[name] {
			environment = append(environment, entry)
		}
	}
	return environment
}
