package deps

import (
	"fmt"
	"io"
)

// StartOutput is what a scripted child does at Start: AtStart sees the argv
// and options the call carried (a file the real child would read before it
// exits is read here, while it still exists), Stdout and Stderr are written
// to the call's StartOptions.Stdout and Stderr, and a non-zero ExitCode makes
// Process.Wait answer an error whose ExitCode() is it. StartOptions.Stdin is
// left unread, so a test reads what the child would have been given from
// Starts().
type StartOutput struct {
	AtStart        func(argv []string, opts StartOptions)
	Stdout, Stderr string
	ExitCode       int
}

// fakeExitStatus is the Wait error of a scripted non-zero exit.
type fakeExitStatus int

func (status fakeExitStatus) Error() string { return fmt.Sprintf("exit status %d", int(status)) }
func (status fakeExitStatus) ExitCode() int { return int(status) }

// ScriptStartOutput registers the output a Start call whose argv starts with
// prefix plays, on a default Process (pid 4242) that exits with ExitCode.
func (f *FakeRunner) ScriptStartOutput(prefix []string, output StartOutput) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startScripts = append(f.startScripts, fakeStartScript{
		prefix: append([]string(nil), prefix...), pid: 4242, output: &output,
	})
}

// play runs the scripted child at Start (a nil output plays nothing) and
// returns the error Wait answers: waitErr, or the scripted exit status.
func (output *StartOutput) play(argv []string, opts StartOptions, waitErr error) error {
	if output == nil {
		return waitErr
	}
	if output.AtStart != nil {
		output.AtStart(append([]string(nil), argv...), opts)
	}
	if opts.Stdout != nil {
		_, _ = io.WriteString(opts.Stdout, output.Stdout)
	}
	if opts.Stderr != nil {
		_, _ = io.WriteString(opts.Stderr, output.Stderr)
	}
	if output.ExitCode != 0 {
		return fakeExitStatus(output.ExitCode)
	}
	return waitErr
}
