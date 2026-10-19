package installer

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// lookupLoginShell reads the current uid's NSS passwd entry, not the tool's environment.
func lookupLoginShell(ctx context.Context, runner deps.Runner, uid int) (string, error) {
	id := strconv.Itoa(uid)
	output, err := loginShellOutput(ctx, runner, "getent", "passwd", id)
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
