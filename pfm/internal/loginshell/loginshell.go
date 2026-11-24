// Package loginshell answers which shell the user's account logs in with, from
// the account database (getent on Linux, Directory Services on macOS), never
// from $SHELL: a tool's shell sets SHELL to its own (Claude Code's Bash tool
// sets it to bash), so the variable names the caller, not the account.
//
//	loginshell.go         this menu and runLookup, the one runner call every lookup uses
//	loginshell_linux.go   Lookup over getent passwd, and ScriptAccount, a fake runner answering it (linux build)
//	loginshell_darwin.go  Lookup over dscl, and ScriptAccount (darwin build)
//	loginshell_dscl.go    lookupDarwin and scriptDarwin, the dscl implementation and its fake's answer, untagged so the
//	                      Linux fence tests them
package loginshell

import (
	"context"
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// runLookup is argv's trimmed stdout; a failed start or a nonzero exit is an error naming argv and stderr.
func runLookup(ctx context.Context, runner deps.Runner, argv ...string) (string, error) {
	result, err := runner.Run(ctx, argv, deps.RunOptions{})
	if err != nil {
		return "", fmt.Errorf("lookup login shell via %q: %w; stderr: %s", argv, err, result.Stderr)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("lookup login shell via %q: exit %d; stderr: %s", argv, result.ExitCode, result.Stderr)
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}
