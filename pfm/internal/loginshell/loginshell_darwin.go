package loginshell

import (
	"context"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// Lookup is the account login shell of uid, read through runner.
func Lookup(ctx context.Context, runner deps.Runner, uid int) (string, error) {
	return lookupDarwin(ctx, runner, uid)
}

// ScriptAccount scripts runner to answer Lookup for uid as Directory Services
// does (scriptDarwin).
func ScriptAccount(runner *deps.FakeRunner, uid int, shell string, err error) [][]string {
	return scriptDarwin(runner, uid, shell, err)
}
