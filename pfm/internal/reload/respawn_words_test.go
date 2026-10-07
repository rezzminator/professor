package reload

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

// Helpers that read a respawn line the way the shell and Claude Code do.

func respawnWords(t *testing.T, run string) []string {
	t.Helper()
	output, err := exec.Command("sh", "-c", "set -- "+run+"; printf '%s\\000' \"$@\"").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
}

func parsedReloadShell(t *testing.T, run string) claudelaunch.Parsed {
	t.Helper()
	parsed, err := claudelaunch.Parse(append([]string{"claude"}, respawnWords(t, run)...))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// respawnEnv is the environment a respawn line assigns its Claude process:
// the NAME=value operands of the leading env word, up to the binary.
func respawnEnv(t *testing.T, run string) map[string]string {
	t.Helper()
	env := map[string]string{}
	words := respawnWords(t, run)
	for index := 0; index < len(words); index++ {
		switch word := words[index]; {
		case index == 0 && word == "env":
		case word == "-u":
			index++
		default:
			name, value, found := strings.Cut(word, "=")
			if !found || name == "" || strings.Contains(name, "/") {
				return env
			}
			env[name] = value
		}
	}
	return env
}
