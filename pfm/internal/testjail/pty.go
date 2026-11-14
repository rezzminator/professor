//go:build jail

package testjail

import (
	"os"
	"os/exec"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/deps"
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

// PTYCommand builds a command that runs argv on a REAL pty via script(1), for
// behaviour that only happens on a terminal. Built only under the jail tag: a
// real child is no unit test's. A script(1) that cannot be
// resolved is the deps.Resolve error, never a bare name left to fail later.
func PTYCommand(argv ...string) (*exec.Cmd, error) {
	scriptBinary, err := deps.Resolve("script")
	if err != nil {
		return nil, err
	}
	scriptArgv := ptyScriptArgv(scriptBinary, argv)
	return exec.Command(scriptArgv[0], scriptArgv[1:]...), nil
}

// bsdScriptEOFEcho is what BSD script(1) leaves in the pty's output when its
// own stdin ends: it writes the EOF character into the pty, whose line
// discipline echoes it under ECHOCTL as "^D" and two backspaces. util-linux
// script writes nothing, so the text never appears on Linux.
const bsdScriptEOFEcho = "^D\b\b"

// PTYOutput is a PTYCommand's output as the command under test wrote it, with
// BSD script(1)'s echoed end-of-input removed, so one expected value holds on
// Linux and macOS.
func PTYOutput(output []byte) string {
	return strings.ReplaceAll(string(output), bsdScriptEOFEcho, "")
}
