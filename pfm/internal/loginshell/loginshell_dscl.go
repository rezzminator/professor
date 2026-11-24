package loginshell

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// lookupDarwin uses Directory Services' search node so network accounts
// and local accounts are both resolved by uid, never by USER or LOGNAME.
func lookupDarwin(ctx context.Context, runner deps.Runner, uid int) (string, error) {
	id := strconv.Itoa(uid)
	output, err := runLookup(ctx, runner, dsclSearchArgv(id)...)
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
	output, err = runLookup(ctx, runner, dsclReadArgv(fields[0])...)
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

// dsclSearchArgv is the Directory Services search for the record of uid id.
func dsclSearchArgv(id string) []string {
	return []string{"dscl", "/Search", "-search", "/Users", "UniqueID", id}
}

// dsclReadArgv is the Directory Services read of account's login shell.
func dsclReadArgv(account string) []string {
	return []string{"dscl", "/Search", "-read", path.Join("/Users", account), "UserShell"}
}

// scriptDarwin scripts runner to answer lookupDarwin for uid as dscl does: the
// search's native array form naming the record, then its UserShell attribute
// holding shell; a failed search (err) ends the lookup there. It returns the
// lookup's calls in order, the first the one a failure names.
func scriptDarwin(runner *deps.FakeRunner, uid int, shell string, err error) [][]string {
	id := strconv.Itoa(uid)
	search := dsclSearchArgv(id)
	runner.Script(search, deps.RunResult{Stdout: []byte("fixture\t\tUniqueID = (\n    " + id + "\n)\n")}, err)
	if err != nil {
		return [][]string{search}
	}
	read := dsclReadArgv("fixture")
	runner.Script(read, deps.RunResult{Stdout: []byte("UserShell: " + shell + "\n")}, nil)
	return [][]string{search, read}
}
