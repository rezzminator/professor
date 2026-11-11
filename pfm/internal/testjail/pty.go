package testjail

import (
	"os"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
)

// The two script(1) implementations disagree about their own argv. util-linux
// takes the command through -c and the typescript file LAST; BSD (macOS) takes
// the typescript file FIRST and then the command and its arguments directly,
// with no -c and no -f. Handing Linux's form to the BSD binary prints a usage
// message and exits — and a test that then writes to its stdin fails with
// "broken pipe", which reads as the code under test closing the pipe rather
// than as the harness being wrong about the platform. pty_{linux,darwin}.go
// select one as ptyScriptArgv.

// utilLinuxScriptArgv is util-linux script(1)'s form: the command, quoted
// into one word, through -c, then the typescript file.
func utilLinuxScriptArgv(script string, argv []string) []string {
	quoted := make([]string, len(argv))
	for index, argument := range argv {
		quoted[index] = cmdparse.Quote(argument)
	}
	return []string{script, "-qefc", strings.Join(quoted, " "), os.DevNull}
}

// bsdScriptArgv is BSD script(1)'s form: the typescript file, then the
// command and its arguments as they are.
func bsdScriptArgv(script string, argv []string) []string {
	return append([]string{script, "-qe", os.DevNull}, argv...)
}
