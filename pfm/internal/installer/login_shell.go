package installer

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// lookupDarwinLoginShell uses Directory Services' search node so network accounts
// and local accounts are both resolved by uid, never by USER or LOGNAME.
func lookupDarwinLoginShell(ctx context.Context, runner deps.Runner, uid int) (string, error) {
	id := strconv.Itoa(uid)
	output, err := loginShellOutput(ctx, runner, "dscl", "/Search", "-search", "/Users", "UniqueID", id)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(output)
	// dscl renders search values as a parenthesized list on Darwin. Older
	// implementations also emit the scalar form; accept exactly one uid.
	if len(fields) == 6 && fields[3] == "(" && fields[5] == ")" {
		fields = []string{fields[0], fields[1], fields[2], fields[4]}
	}
	if len(fields) != 4 || fields[1] != "UniqueID" || fields[2] != "=" || fields[3] != id ||
		fields[0] == "." || fields[0] == ".." || strings.Contains(fields[0], "/") {
		return "", fmt.Errorf("dscl search for uid %s returned an invalid record: %q", id, output)
	}
	record := path.Join("/Users", fields[0])
	output, err = loginShellOutput(ctx, runner, "dscl", "/Search", "-read", record, "UserShell")
	if err != nil {
		return "", err
	}
	shell, found := strings.CutPrefix(output, "UserShell:")
	// A single value containing spaces is printed on the line after the key.
	shell = strings.TrimSpace(shell)
	if !found || shell == "" || strings.ContainsAny(shell, "\r\n") {
		return "", fmt.Errorf("dscl UserShell for uid %s returned an invalid attribute: %q", id, output)
	}
	return shell, nil
}
