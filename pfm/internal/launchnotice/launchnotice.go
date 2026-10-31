// Package launchnotice tells the person who starts a chat what the launch
// could not carry, and holds the screen until it is read where the next step
// would cover it.
package launchnotice

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/x/term"
)

// EnterPrompt is the line printed before the wait.
const EnterPrompt = "press Enter to continue"

var (
	stdin io.Reader = os.Stdin
	// isTerminal reports whether in and out are both a terminal.
	isTerminal = func(in io.Reader, out io.Writer) bool {
		inFile, inOK := in.(*os.File)
		outFile, outOK := out.(*os.File)
		return inOK && outOK && term.IsTerminal(inFile.Fd()) && term.IsTerminal(outFile.Fd())
	}
)

// Replace makes Hold read from in and treat the screen as a terminal or not,
// and returns the undo. It is the seam a test of a door uses.
func Replace(in io.Reader, terminal bool) (restore func()) {
	previousIn, previousCheck := stdin, isTerminal
	stdin = in
	isTerminal = func(io.Reader, io.Writer) bool { return terminal }
	return func() { stdin, isTerminal = previousIn, previousCheck }
}

// Print writes each warning to out as "who: warning". It never waits.
func Print(out io.Writer, who string, warnings []string) {
	for _, warning := range warnings {
		fmt.Fprintf(out, "%s: %s\n", who, warning)
	}
}

// Hold is Print for a surface that attaches next: on a terminal it then asks
// for Enter and reads one line, so the attach cannot cover the warning
// unread. Off a terminal it only prints; with no warning it does nothing.
func Hold(out io.Writer, who string, warnings []string) {
	Print(out, who, warnings)
	Pause(out, who, warnings)
}

// Pause is the wait of Hold for warnings the surface has already printed: on a
// terminal, with a warning to read, it asks for Enter and reads one line.
func Pause(out io.Writer, who string, warnings []string) {
	if len(warnings) == 0 || !isTerminal(stdin, out) {
		return
	}
	fmt.Fprintln(out, EnterPrompt)
	if _, err := bufio.NewReader(stdin).ReadString('\n'); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(out, "%s: read the acknowledgement: %v — continuing\n", who, err)
	}
}

// HoldCancellable is Hold for a surface that has already made what the wait
// must not orphan: an interrupt, hangup or terminate during the wait returns
// cancelled instead of killing the process, so the caller undoes what it made.
func HoldCancellable(out io.Writer, who string, warnings []string) (cancelled bool) {
	if len(warnings) == 0 || !isTerminal(stdin, out) {
		Print(out, who, warnings)
		return false
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(signals)
	held := make(chan struct{})
	go func() {
		defer close(held)
		Hold(out, who, warnings)
	}()
	select {
	case <-held:
		return false
	case <-signals:
		return true
	}
}

// Relocate is the one missing-directory rule of every door that brings a chat
// back: the recorded directory when it still is one, otherwise the fleet home
// and the notice that says so. No directory recorded counts as missing.
func Relocate(recorded, home string) (dir, notice string) {
	if recorded != "" {
		if info, err := os.Stat(recorded); err == nil && info.IsDir() {
			return recorded, ""
		}
	}
	why := "no directory of its own was recorded"
	if recorded != "" {
		why = "its own directory " + recorded + " is gone"
	}
	return home, fmt.Sprintf("opened in %s: %s", home, why)
}
