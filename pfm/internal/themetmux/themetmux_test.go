package themetmux

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

type fakeProbe struct {
	pidErr map[string]error
}

func (probe *fakeProbe) ServerPID(_ context.Context, socket string) (int, error) {
	if err := probe.pidErr[socket]; err != nil {
		return 0, err
	}
	return 42, nil
}

func (*fakeProbe) ShowGlobalOption(context.Context, string, string) (string, error) { return "", nil }

func (*fakeProbe) ApplyGlobalOptions(context.Context, string, [][]string) error { return nil }

func listenSocket(t *testing.T, directory, name string) {
	t.Helper()
	listener, err := net.Listen("unix", filepath.Join(directory, name))
	if err != nil {
		t.Fatalf("listen on %s: %v", name, err)
	}
	t.Cleanup(func() { _ = listener.Close() })
}

func TestSockets(t *testing.T) {
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "1")
	directory := testjail.ShortRoot(t)
	for _, name := range []string{"probe-cc-2", "probe-cc-1", "probe-cc-gone", "probe-cc-sick", "vsct-editor", "default"} {
		listenSocket(t, directory, name)
	}
	if err := os.WriteFile(filepath.Join(directory, "probe-cc-file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sick := errors.New("tmux answered a garbled pid")
	probe := &fakeProbe{pidErr: map[string]error{
		"probe-cc-gone": fmt.Errorf("read pid: %w", gather.ErrServerGone),
		"probe-cc-sick": sick,
	}}

	got, err := Servers{Probe: probe, TmuxDir: directory}.Sockets(context.Background())

	want := []string{"probe-cc-1", "probe-cc-2", "probe-cc-sick"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf(
			"Sockets = %v, want %v (non-chat names, a regular file and a gone server drop; a server whose liveness failed stays)",
			got,
			want,
		)
	}
	if !errors.Is(err, sick) {
		t.Fatalf("Sockets error = %v, want the liveness failure of probe-cc-sick joined", err)
	}
}

// TestSocketsStatFaults: a socket whose stat failed is a named fault, never
// "no server"; one that vanished between the listing and the stat is skipped.
func TestSocketsStatFaults(t *testing.T) {
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "1")
	directory := testjail.ShortRoot(t)
	listenSocket(t, directory, "probe-cc-1")
	listenSocket(t, directory, "probe-cc-2")
	denied := fmt.Errorf("lstat: %w", fs.ErrPermission)
	for _, tc := range []struct {
		name    string
		statErr error
		wantErr bool
	}{
		{name: "permission fault is named", statErr: denied, wantErr: true},
		{name: "a vanished socket is skipped", statErr: fmt.Errorf("lstat: %w", fs.ErrNotExist)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			servers := Servers{Probe: &fakeProbe{}, TmuxDir: directory}
			servers.entryInfo = func(entry fs.DirEntry) (fs.FileInfo, error) {
				if entry.Name() == "probe-cc-1" {
					return nil, tc.statErr
				}
				return entry.Info()
			}

			got, err := servers.Sockets(context.Background())

			if !reflect.DeepEqual(got, []string{"probe-cc-2"}) {
				t.Fatalf("Sockets = %v, want only the socket that could be stat-ed", got)
			}
			if tc.wantErr != (err != nil) {
				t.Fatalf("Sockets error = %v, want an error: %v", err, tc.wantErr)
			}
			if tc.wantErr && (!errors.Is(err, fs.ErrPermission) ||
				!strings.Contains(err.Error(), filepath.Join(directory, "probe-cc-1"))) {
				t.Fatalf("Sockets error = %v, want the permission fault naming the socket path", err)
			}
		})
	}
}

func TestSocketsMissingDirectoryIsNoServers(t *testing.T) {
	got, err := Servers{
		Probe:   &fakeProbe{},
		TmuxDir: filepath.Join(testjail.ShortRoot(t), "absent"),
	}.Sockets(
		context.Background(),
	)
	if err != nil || len(got) != 0 {
		t.Fatalf("Sockets over a missing directory = %v, %v; want none and no error", got, err)
	}
}
