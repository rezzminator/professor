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
