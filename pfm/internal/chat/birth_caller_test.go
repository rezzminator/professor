package chat

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A door whose caller is the process running it is placed by that process's
// own lookup, read against its environment: a plain process is outside Herdr,
// a Herdr pane claim or an engine's shell no pane proves is unproven; with no
// Herdr server at its environment's socket each is hosted in tmux.
func TestWithLocalCallerIdentifiesTheProcess(t *testing.T) {
	notInChat := resolve.ErrNotInChat.Error()
	for _, tc := range []struct {
		name, herdrPane, session, callerErr string
	}{
		{name: "plain process", callerErr: notInChat},
		{
			name: "Herdr claim no pane proves", herdrPane: "w6:p3",
			callerErr: "caller unproven: its environment names Herdr pane w6:p3, which its lookup did not prove (" +
				notInChat + " (stale: herdr SOCKET w6:p3: herdr socket SOCKET: dial for pane.get pfm-seat pane w6:p3: " +
				"dial unix SOCKET: connect: no such file or directory))",
		},
		{
			name: "engine shell no pane holds", session: "a1000000-1111-4111-8111-111111111113",
			callerErr: "caller unproven: cc session a1000000-1111-4111-8111-111111111113 runs in no pane pfm can prove (" +
				notInChat + ")",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testjail.Fleet(t)
			socket := filepath.Join(testjail.ShortRoot(t), "absent.sock")
			for name, value := range map[string]string{
				"TMUX": "", "TMUX_PANE": "", "HERDR_SOCKET_PATH": socket, "HERDR_PANE_ID": tc.herdrPane,
				"CLAUDE_CODE_SESSION_ID": tc.session, "CODEX_THREAD_ID": "",
			} {
				t.Setenv(name, value)
			}
			ctx, recorder := obs.Test(t)
			choice, err := BirthBackend(WithLocalCaller(ctx, "mcp.new"), "cli.new", "auto")
			if choice.Backend != "tmux" || err != nil {
				t.Fatalf("choice=%+v error=%v; want tmux", choice, err)
			}
			records := recorder.Records()
			last := records[len(records)-1]
			callerErr, _ := last.Field("caller_err")
			read, _ := last.Field("herdr_server")
			wantRead := "down: no Herdr server runs at " + socket + ": stat " + socket + ": no such file or directory"
			wantErr := strings.ReplaceAll(tc.callerErr, "SOCKET", socket)
			if callerErr != wantErr || read != wantRead {
				t.Fatalf("caller_err=%v herdr_server=%v; want %q, %q", callerErr, read, wantErr, wantRead)
			}
			if door, _ := records[len(records)-1].Field("door"); door != "mcp.new" {
				t.Fatalf("placement door=%v, want mcp.new", door)
			}
		})
	}
}
