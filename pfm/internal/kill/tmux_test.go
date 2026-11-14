package kill

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// requireTmuxRecord asserts the one comp=tmux record a façade call writes:
// subcmd, target when given, exit 0 and dur_ms.
func requireTmuxRecord(t *testing.T, recorder *obs.Recorder, index int, subcmd, target string) {
	t.Helper()
	records := recorder.Records()
	if len(records) <= index {
		t.Fatalf("records = %d, want at least %d: %s", len(records), index+1, recorder.Raw())
	}
	record := records[index]
	if record.Message != "tmux.exec" {
		t.Fatalf("record %d = %s, want tmux.exec", index, record.Message)
	}
	if comp, _ := record.Field(obs.FieldComp); comp != "tmux" {
		t.Fatalf("comp = %v, want tmux", comp)
	}
	if got, _ := record.Field("subcmd"); got != subcmd {
		t.Fatalf("subcmd = %v, want %s", got, subcmd)
	}
	if got, _ := record.Field("target"); target != "" && got != target {
		t.Fatalf("target = %v, want %s", got, target)
	}
	if exit, _ := record.Field(obs.FieldExit); exit != float64(0) {
		t.Fatalf("exit = %v, want 0", exit)
	}
	if _, found := record.Field(obs.FieldDur); !found {
		t.Fatalf("no dur_ms: %v", record.Fields)
	}
}

func TestPaneExistsPropagatesConnectionFailure(t *testing.T) {
	for _, test := range []struct {
		message string
		wantErr bool
	}{
		{"error connecting to /x (Permission denied)", true},
		{"protocol version mismatch", true},
		{"no server running on /x", false},
		{"error connecting to /x (No such file or directory)", false},
		{"error connecting to /x (Connection refused)", false},
	} {
		t.Run(test.message, func(t *testing.T) {
			tmux := testjail.TmuxRunner(deps.RunResult{Stderr: []byte(test.message + "\n"), ExitCode: 1})
			exists, err := (TmuxKiller{}).PaneExists(pfmtmux.WithClientRunner(context.Background(), tmux), "/x", "%4")
			if exists || (err != nil) != test.wantErr {
				t.Fatalf("PaneExists = %v, %v, want false, error=%v", exists, err, test.wantErr)
			}
			if test.wantErr && !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error %v does not carry stderr %q", err, test.message)
			}
		})
	}
}

// TestTmuxKillerRecordsEveryInvocation: the kill façade terminates through
// the observed tmux command — one record per call.
func TestTmuxKillerRecordsEveryInvocation(t *testing.T) {
	ctx, recorder := obs.Test(t)
	ctx = pfmtmux.WithClientRunner(ctx, testjail.TmuxRunner(deps.RunResult{}))
	tmux := TmuxKiller{Binary: "tmux"}
	if err := tmux.KillPane(ctx, "/sockets/cc-1", "%3"); err != nil {
		t.Fatal(err)
	}
	if err := tmux.KillServer(ctx, "/sockets/cc-1"); err != nil {
		t.Fatal(err)
	}
	requireTmuxRecord(t, recorder, 0, "kill-pane", "%3")
	requireTmuxRecord(t, recorder, 1, "kill-server", "")
	if len(recorder.Records()) != 2 {
		t.Fatalf("records = %d, want 2: %s", len(recorder.Records()), recorder.Raw())
	}
	_ = context.Background
}

func TestParseClientsSkipsOnlyTheUnreadableLine(t *testing.T) {
	clients, skipped := parseClients("101 /dev/pts/1\nnot-a-pid\n\n102 client-102\n103\n")
	wantClients := []Client{{PID: 101, Name: "/dev/pts/1"}, {PID: 102, Name: "client-102"}}
	if !reflect.DeepEqual(clients, wantClients) {
		t.Fatalf("clients = %v, want %v", clients, wantClients)
	}
	want := []string{
		`client pid "not-a-pid" unreadable: strconv.Atoi: parsing "not-a-pid": invalid syntax`,
		"client 103: tmux listed it with no name to detach it by",
	}
	if !reflect.DeepEqual(skipped, want) {
		t.Fatalf("skipped = %#v, want %#v", skipped, want)
	}
}
