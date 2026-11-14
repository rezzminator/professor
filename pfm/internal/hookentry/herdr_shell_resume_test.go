package hookentry

import (
	"bytes"
	"net"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestResumePendingAnswersTheShimAndLogsWhatItCouldNotRead: the shim's
// auto-open yields ("pending") only to a pane Herdr restored with its agent's
// session; a fresh pane and a shell outside Herdr open the picker ("no")
// with nothing logged, and a Herdr that cannot be asked opens it too with one
// WARN naming the pane and the error.
func TestResumePendingAnswersTheShimAndLogsWhatItCouldNotRead(t *testing.T) {
	socket := filepath.Join(testjail.ShortRoot(t), "h.sock")
	answer := ""
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			buffer := make([]byte, 4096)
			_, _ = conn.Read(buffer)
			_, _ = conn.Write([]byte(`{"id":"pfm-seat","result":` + answer + "}\n"))
			_ = conn.Close()
		}
	}()
	restored := `{"type":"pane_info","pane":{"pane_id":"w1:p1","agent_session":{"source":"herdr:claude","agent":"claude","kind":"id","value":"0b5c"}}}`
	for _, testCase := range []struct {
		name, answer, socket string
		marker               bool
		want                 string
		warn                 string
	}{
		{name: "restored pane", answer: restored, socket: socket, marker: true, want: "pending\n"},
		{name: "fresh pane", answer: `{"type":"pane_info","pane":{"pane_id":"w1:p1"}}`, socket: socket, marker: true, want: "no\n"},
		{name: "outside Herdr", answer: restored, socket: socket, want: "no\n"},
		{
			name: "Herdr unreachable", socket: filepath.Join(testjail.ShortRoot(t), "gone.sock"), marker: true, want: "no\n",
			warn: "herdr restore: could not read whether Herdr resumes this pane's agent; the picker opens",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			answer = testCase.answer
			ctx, recorder := obs.Test(t)
			env := &paths.MapEnv{Values: map[string]string{
				paths.EnvHome: t.TempDir(), "HERDR_SOCKET_PATH": testCase.socket, "HERDR_PANE_ID": "w1:p1",
			}}
			if testCase.marker {
				env.Values["HERDR_ENV"] = "1"
			}
			var stdout, stderr bytes.Buffer
			code := herdrShellResume(ctx, nil, &stdout, &stderr, env)
			if code != 0 || stdout.String() != testCase.want || stderr.Len() != 0 {
				t.Fatalf("resume-pending = exit %d, stdout %q, stderr %q; want exit 0, %q and no stderr",
					code, stdout.String(), stderr.String(), testCase.want)
			}
			records := recorder.Records()
			if testCase.warn == "" {
				if len(records) != 0 {
					t.Fatalf("records = %+v, want none", records)
				}
				return
			}
			if len(records) != 1 || records[0].Level != "WARN" || records[0].Message != testCase.warn {
				t.Fatalf("records = %+v, want one WARN %q", records, testCase.warn)
			}
			if pane, _ := records[0].Field("pane"); pane != "w1:p1" {
				t.Fatalf("WARN pane = %v, want w1:p1", pane)
			}
			if cause, _ := records[0].Field(obs.FieldErr); cause == nil || cause == "" {
				t.Fatalf("WARN carries no error: %+v", records[0])
			}
		})
	}
}
