package loginshell

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// Lookup reads the current uid's NSS passwd entry, not the tool's environment.
func Lookup(ctx context.Context, runner deps.Runner, uid int) (string, error) {
	id := strconv.Itoa(uid)
	output, err := runLookup(ctx, runner, getentArgv(id)...)
	if err != nil {
		return "", err
	}
	fields := strings.Split(output, ":")
	if len(fields) != 7 || fields[2] != id || strings.ContainsAny(output, "\r\n") {
		return "", fmt.Errorf("getent passwd for uid %s returned an invalid entry: %q", id, output)
	}
	// passwd(5): an empty login-shell field means the Bourne shell.
	if fields[6] == "" {
		return "/bin/sh", nil
	}
	return fields[6], nil
}

// getentArgv is the account-database read Lookup runs for uid id.
func getentArgv(id string) []string { return []string{"getent", "passwd", id} }

// ScriptAccount scripts runner to answer Lookup for uid as getent does: the
// account's passwd entry with shell as its login shell, or err. It returns
// the lookup's calls in order, the first the one a failure names.
func ScriptAccount(runner *deps.FakeRunner, uid int, shell string, err error) [][]string {
	id := strconv.Itoa(uid)
	argv := getentArgv(id)
	runner.Script(argv, deps.RunResult{Stdout: []byte("fixture:x:" + id + ":20:Fixture:/fixture:" + shell + "\n")}, err)
	return [][]string{argv}
}
