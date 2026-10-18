package picker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A chat the picker opens in a Herdr tab of its own leaves nothing for the
// picker's terminal to run, so the picker's own pane closes; an eval line, a
// failed open, or a picker outside Herdr keeps the pane.
func TestClosePickerPaneAfterHerdrOpen(t *testing.T) {
	tests := []struct {
		name      string
		inHerdr   bool
		evalLine  string
		code      int
		wantClose bool
	}{
		{name: "unproved picker stays open after successful chat open", inHerdr: true},
		{name: "eval line for this terminal keeps it", inHerdr: true, evalLine: "exec tmux attach"},
		{name: "failed open keeps it", inHerdr: true, code: 1},
		{name: "outside herdr keeps it"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(paths.EnvHome, t.TempDir())
			t.Setenv(paths.EnvStateDB, filepath.Join(t.TempDir(), "absent.db"))
			socket := filepath.Join(testjail.ShortRoot(t), "pick.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			requests := make(chan map[string]any, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				var req map[string]any
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				requests <- req
				_, _ = fmt.Fprintf(conn, `{"id":%q,"ok":true,"result":{"type":"ok"}}`+"\n", req["id"])
			}()
			marker := ""
			if test.inHerdr {
				marker = "1"
			}
			t.Setenv(herdr.EnvMarker, marker)
			t.Setenv(herdr.EnvSocket, socket)
			t.Setenv(herdr.EnvPane, "w2:p5")
			var stdout, stderr bytes.Buffer
			code := closePickerPaneAfter(context.Background(), &stdout, &stderr, func(out io.Writer) int {
				if test.evalLine != "" {
					_, _ = fmt.Fprintln(out, test.evalLine)
				}
				return test.code
			})
			if code != test.code {
				t.Fatalf("code=%d want %d", code, test.code)
			}
			if test.inHerdr && test.evalLine == "" && test.code == 0 &&
				!strings.Contains(stderr.String(), "ownership unproved") {
				t.Fatalf("unproved successful open must name why picker stayed: %s", &stderr)
			}
			if stdout.String() != lineOrEmpty(test.evalLine) {
				t.Fatalf("stdout=%q, the eval line must pass through unchanged", stdout.String())
			}
			select {
			case req := <-requests:
				if !test.wantClose {
					t.Fatalf("picker pane closed when it must stay: %v", req)
				}
				params, _ := req["params"].(map[string]any)
				if req["method"] != "pane.close" || params["pane_id"] != "w2:p5" {
					t.Fatalf("close request=%v", req)
				}
			case <-time.After(300 * time.Millisecond):
				if test.wantClose {
					t.Fatalf("picker pane never closed; stderr=%q", stderr.String())
				}
			}
		})
	}
}

func lineOrEmpty(line string) string {
	if line == "" {
		return ""
	}
	return line + "\n"
}
