package spawn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestChooseBackend(t *testing.T) {
	live := backendEnvironment{Marker: "1", Socket: "/tmp/x/herdr.sock", Pane: "w6:p3"}
	for _, tc := range []struct {
		name               string
		env                backendEnvironment
		probeErr           error
		want, warning, err string
	}{
		{name: "native", env: live, want: "herdr"},
		{name: "nested tmux", env: backendEnvironment{Marker: "1", Socket: live.Socket, Pane: live.Pane, TMUX: "/tmp/tmux-501/cc-1-2-3,41202,0"}, want: "tmux"},
		{name: "plain shell", env: backendEnvironment{}, want: "tmux"},
		{name: "dead socket", env: live, probeErr: errors.New("dial unix /tmp/x/herdr.sock: connect: no such file or directory"), want: "tmux", warning: "pfm: Herdr pane w6:p3 unreachable (dial unix /tmp/x/herdr.sock: connect: no such file or directory) — starting in tmux"},
		{name: "missing pane", env: live, probeErr: errors.New("pane_not_found"), want: "tmux", warning: "pane_not_found"},
		{name: "forced unreachable", env: backendEnvironment{Requested: "herdr", Socket: live.Socket, Pane: live.Pane}, probeErr: errors.New("dead socket"), err: "dead socket"},
		{name: "forced tmux", env: backendEnvironment{Requested: "tmux", Marker: "1", Socket: live.Socket, Pane: live.Pane}, want: "tmux"},
		{name: "requester", env: backendEnvironment{Requester: &fleetdb.Seat{Backend: "herdr", HerdrSocket: live.Socket, HerdrPane: live.Pane, HerdrWorkspace: "w6"}}, want: "herdr"},
		{name: "invalid", env: backendEnvironment{Requested: "other"}, err: "backend"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectBackend(tc.env, func(string, string) error { return tc.probeErr })
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || got.Backend != tc.want {
				t.Fatalf("choice = %+v,%v; want %s", got, err, tc.want)
			}
			if tc.warning != "" && !strings.Contains(got.Warning, tc.warning) {
				t.Fatalf("warning = %q, want %q", got.Warning, tc.warning)
			}
			if tc.want == "herdr" &&
				(got.HerdrSocket != live.Socket || got.HerdrPane != live.Pane || got.HerdrWorkspace != "w6") {
				t.Fatalf("native location = %+v", got)
			}
		})
	}
}

func TestChooseBackendRejectsMalformedPane(t *testing.T) {
	for _, body := range []string{`{}`, `{"pane":{"pane_id":"w9:p9"}}`} {
		t.Run(body, func(t *testing.T) {
			socket := filepath.Join(testjail.ShortRoot(t), "h.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				var req struct {
					ID string `json:"id"`
				}
				if json.NewDecoder(conn).Decode(&req) == nil {
					_, _ = fmt.Fprintf(conn, "{\"id\":%q,\"result\":%s}\n", req.ID, body)
				}
			}()
			_, err = ChooseBackend(
				context.Background(),
				"herdr",
				[]string{"HERDR_SOCKET_PATH=" + socket, "HERDR_PANE_ID=w1:p2"},
				nil,
			)
			if err == nil {
				t.Fatal("malformed pane selected native birth")
			}
		})
	}
}
