package deps

import (
	"context"
	"net"
	"time"
)

// DialUnix opens a client connection to the unix socket at path, the one door
// a local socket client (the Herdr reporter) crosses instead of net.Dialer.
func DialUnix(ctx context.Context, path string, timeout time.Duration) (net.Conn, error) {
	dialer := net.Dialer{Timeout: timeout}
	return dialer.DialContext(ctx, "unix", path)
}
