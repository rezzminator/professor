package herdr

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

// ServerUp tells a running server, a socket nothing listens at and a socket
// that gives no usable answer apart: only the second reads as down.
func TestServerUp(t *testing.T) {
	answering := func(result string) string {
		return serve(t, func(string) string { return `{"result":` + result + `}` })
	}
	absent := filepath.Join(t.TempDir(), "herdr.sock")
	stale := filepath.Join(t.TempDir(), "herdr.sock")
	listener, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, socket string
		want         string // the whole error, or its prefix when down is false and the cause is the wire's
		down         bool
	}{
		{name: "running", socket: answering(`{"type":"pong","version":"0.9.5","protocol":22}`)},
		{
			name: "absent socket", socket: absent, down: true,
			want: "no Herdr server runs at " + absent + ": stat " + absent + ": no such file or directory",
		},
		{
			name: "refused connection", socket: stale, down: true,
			want: "no Herdr server runs at " + stale + ": herdr socket " + stale + ": dial for ping pfm-seat: dial unix " +
				stale + ": connect: connection refused",
		},
		{name: "no usable answer", socket: answering(`null`), want: "read the Herdr server at "},
		{name: "another protocol", socket: answering(`{"type":"pong","protocol":23}`), want: "read the Herdr server at "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ServerUp(context.Background(), tc.socket)
			got := ""
			if err != nil {
				got = err.Error()
			}
			matches := got == tc.want
			if !tc.down && tc.want != "" {
				matches = strings.HasPrefix(got, tc.want+tc.socket+": ")
			}
			if !matches || errors.Is(err, ErrServerDown) != tc.down {
				t.Fatalf("ServerUp(%s) = %q (down %v), want %q (down %v)",
					tc.socket, got, errors.Is(err, ErrServerDown), tc.want, tc.down)
			}
		})
	}
}
