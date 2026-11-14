package hookentry

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"syscall"
)

// seatRetryChoice asks the pane what a failed engine start does next: Enter
// retries, q closes the seat (quit). asked is false when nobody could answer,
// and code is then the host's exit status.
func seatRetryChoice(input *bufio.Reader, tty int, stderr io.Writer) (quit bool, code int, asked bool) {
	for {
		fmt.Fprintln(stderr, "press Enter to retry, q to close")
		line, err := input.ReadString('\n')
		if err != nil {
			// A pane closed while its engine was starting has nobody to ask:
			// that is the hangup's restoration, never a failed retry prompt.
			if hungUp, probeErr := seatTerminalHungUp(tty); probeErr == nil && hungUp {
				return false, 128 + int(syscall.SIGHUP), false
			}
			fmt.Fprintf(stderr, "pfm internal seat-host: read retry choice: %v\n", err)
			return false, 1, false
		}
		switch strings.TrimSpace(line) {
		case "q":
			return true, 0, true
		case "":
			return false, 0, true
		}
	}
}
