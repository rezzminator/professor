package tmux

import (
	"fmt"
	"net"
	"time"
)

// acceptsTimeout bounds one connect(): a unix connect to a dead socket refuses
// at once; the bound is for a server too wedged to accept.
const acceptsTimeout = 200 * time.Millisecond

// Accepts reports, with a connect() and no fork or exec, whether a server is
// still listening on the tmux socket at socketPath. The connection is closed
// at once without speaking the tmux protocol, so the server only sees a client
// come and go. A nil error is a server that answered; any error is a socket
// with nothing behind it (or a path that cannot be reached), which the caller
// reads as "down" — never as a reason to touch the file.
func Accepts(socketPath string) error {
	connection, err := net.DialTimeout("unix", socketPath, acceptsTimeout)
	if err != nil {
		return fmt.Errorf("connect tmux socket %q: %w", socketPath, err)
	}
	if err := connection.Close(); err != nil {
		return fmt.Errorf("close tmux socket %q: %w", socketPath, err)
	}
	return nil
}
