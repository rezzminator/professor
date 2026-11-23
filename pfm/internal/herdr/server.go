package herdr

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// ErrServerDown is a Herdr socket no server listens at: the socket is absent
// or refuses the connection.
var ErrServerDown = errors.New("no Herdr server runs")

// ServerStart reads the start identity of the Herdr server at socket: its
// socket file's mtime, in Unix nanoseconds.
func ServerStart(socket string) (int64, error) {
	info, err := os.Stat(socket)
	if err != nil {
		return 0, fmt.Errorf("stat Herdr socket %s: %w", socket, err)
	}
	return info.ModTime().UnixNano(), nil
}

// ServerUp reads whether a Herdr server answers at socket by its ping: nil
// when it answers with the supported protocol, ErrServerDown when nothing
// listens there, and any other error when pfm cannot tell (a socket that
// gives no usable answer, or a server of another protocol).
func ServerUp(ctx context.Context, socket string) error {
	if _, err := os.Stat(socket); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w at %s: %w", ErrServerDown, socket, err)
	}
	_, err := ProbeProtocol(ctx, socket)
	switch {
	case err == nil:
		return nil
	case SocketGone(err):
		return fmt.Errorf("%w at %s: %w", ErrServerDown, socket, err)
	}
	return fmt.Errorf("read the Herdr server at %s: %w", socket, err)
}
