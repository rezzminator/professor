package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestListNeverListsThePickerPlaceholders pins which rows ls drops: only
// the picker's "start a new chat" actions, which have no chat behind them. A
// Booting row is a real chat with a live pane — it answers call by name — so
// it is listed, never reported absent for its first minute.
func TestListNeverListsThePickerPlaceholders(t *testing.T) {
	for _, kind := range []compose.Kind{compose.NewClaude, compose.NewCodex, compose.NewOpenCode} {
		if !placeholderRow(kind) {
			t.Errorf("placeholder kind %s would be listed", kind)
		}
	}
	for _, kind := range []compose.Kind{
		compose.Booting, compose.LiveClaude, compose.LiveCodex, compose.LiveSplit, compose.Agent,
		compose.ResumeClaude, compose.ResumeCodex, compose.ResumeOpenCode,
	} {
		if placeholderRow(kind) {
			t.Errorf("real row kind %s would be dropped", kind)
		}
	}
}

// TestSelectLiveOnlyDropsKilledAndResumableRows pins the CLI's live listing:
// a killed live chat (by tombstone or by name) is counted, never listed, and a
// resumable row is neither.
func TestSelectLiveOnlyDropsKilledAndResumableRows(t *testing.T) {
	rows := []compose.Row{
		{Kind: compose.LiveClaude, ID: "live", CWD: "/work/alpha"},
		{Kind: compose.LiveClaude, ID: "killed", CWD: "/work/alpha", Killed: true},
		{Kind: compose.LiveCodex, ID: "name-killed", CWD: "/work/alpha", NameKilled: true},
		{Kind: compose.ResumeClaude, ID: "resumable", CWD: "/work/alpha"},
	}
	result := Select(rows, 9, ListRequest{LiveOnly: true})
	if len(result.Rows) != 1 || result.Rows[0].ID != "live" || result.KilledCount != 2 {
		t.Fatalf("live-only select = %+v, want only the live row and 2 killed", result)
	}
	if every := Select(rows, 9, ListRequest{}); len(every.Rows) != 4 || every.KilledCount != 9 {
		t.Fatalf(
			"unfiltered select = %d rows killed %d, want 4 rows and the scan's tally",
			len(every.Rows),
			every.KilledCount,
		)
	}
}

// TestRowsPresenceAddressesEachRowsTmuxServerAndNamesItsFailure pins the
// row-to-seat mapping: a row with a socket is read through its tmux session on
// the socket under the tmux directory, a resumable row (no socket) is in no
// Herdr, and an unaddressable socket is that row's herdr error.
func TestRowsPresenceAddressesEachRowsTmuxServerAndNamesItsFailure(t *testing.T) {
	tmuxDir := t.TempDir()
	values := paths.Values{TmuxDir: tmuxDir}
	stored, err := json.Marshal(herdr.Stored{
		Clients: map[string]herdr.Client{"1": {Pane: "w6:p2", Terminal: "term_a", Socket: "/no/such/herdr.sock"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := []compose.Row{
		{Kind: compose.LiveClaude, Name: "registered", Socket: "cc-1", SessionName: "sess-1", PaneID: "%1"},
		{Kind: compose.ResumeClaude, Name: "resumable"},
		{Kind: compose.LiveClaude, Name: "escaping", Socket: "../cc-2", SessionName: "sess-2"},
		{Kind: compose.LiveClaude, Name: "unset", Socket: "cc-3", SessionName: "sess-3"},
	}
	var ran []string
	probe := herdr.Probe{
		Getenv:   func(string) string { return "/bin/herdr" },
		LookPath: func(string) (string, error) { return "", errors.New("unused") },
		Tmux: func(_ context.Context, socketPath string, args ...string) (string, error) {
			ran = append(ran, socketPath+" "+strings.Join(args, " "))
			if filepath.Base(socketPath) == "cc-1" && args[0] == "show-options" {
				return string(stored), nil
			}
			return "", nil
		},
		Call: func(_ context.Context, socket string, _ ...[]byte) ([]json.RawMessage, error) {
			return nil, errors.New("dial " + socket + ": refused")
		},
	}
	got := rowsPresence(context.Background(), probe, values, rows)
	if len(got) != len(rows) {
		t.Fatalf("rowsPresence = %q, want one entry per row", got)
	}
	if !strings.HasPrefix(got[0], "herdr unreachable: ") || !strings.Contains(got[0], "refused") {
		t.Errorf("registered row with an unreachable Herdr = %q, want herdr unreachable naming the refusal", got[0])
	}
	if got[1] != herdr.PresenceNotInHerdr {
		t.Errorf("resumable row = %q, want %q", got[1], herdr.PresenceNotInHerdr)
	}
	if !strings.HasPrefix(got[2], "herdr error: ") || !strings.Contains(got[2], "escaping") {
		t.Errorf("row with an unaddressable socket = %q, want herdr error naming the row", got[2])
	}
	if got[3] != herdr.PresenceNotInHerdr {
		t.Errorf("row whose session holds no %s = %q, want %q", herdr.StoredOption, got[3], herdr.PresenceNotInHerdr)
	}
	wantRan := []string{
		filepath.Join(tmuxDir, "cc-1") + " show-options -qv -t sess-1 @pfm_herdr",
		filepath.Join(tmuxDir, "cc-1") + " list-clients -t sess-1 -F #{client_pid}",
		filepath.Join(tmuxDir, "cc-3") + " show-options -qv -t sess-3 @pfm_herdr",
		filepath.Join(tmuxDir, "cc-3") + " list-clients -t sess-3 -F #{client_pid}",
	}
	if strings.Join(ran, "|") != strings.Join(wantRan, "|") {
		t.Errorf("tmux commands = %q, want %q", ran, wantRan)
	}
}

// TestPresenceOfNeverReadsAnUnreadRowAsAbsence pins the accessor both surfaces
// share: a row whose presence was not read says so, never an empty string.
func TestPresenceOfNeverReadsAnUnreadRowAsAbsence(t *testing.T) {
	result := ListResult{Herdr: []string{"w6:p2 .professor idle pfm-1"}}
	if got := result.PresenceOf(0); got != "w6:p2 .professor idle pfm-1" {
		t.Errorf("PresenceOf(0) = %q, want the row's own presence", got)
	}
	for _, index := range []int{-1, 1} {
		if got := result.PresenceOf(index); !strings.HasPrefix(got, "herdr error: ") {
			t.Errorf("PresenceOf(%d) = %q, want a herdr error for a row never read", index, got)
		}
	}
}

// TestPresenceTmuxReadGivesUpOnAFrozenServer pins the presence read's bound: a
// chat server that accepts its socket and never answers (stopped or wedged)
// fails the read within gather.ProbeTimeout, as the fleet scan's own probe
// does, so one frozen server never hangs `pfm chat ls` or the MCP ls.
func TestPresenceTmuxReadGivesUpOnAFrozenServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	socketPath := filepath.Join(testjail.ShortRoot(t), "cc-1")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	held := make(chan net.Conn, 16)
	t.Cleanup(func() {
		_ = listener.Close()
		for len(held) > 0 {
			_ = (<-held).Close()
		}
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			held <- conn // held open, never answered
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := runTmux(context.Background(), socketPath, "show-options", "-qv", "-t", "cc-1", herdr.StoredOption)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), gather.ProbeTimeout.String()) {
			t.Fatalf("read of a frozen server = %v, want an error naming the %s bound", err, gather.ProbeTimeout)
		}
	case <-time.After(gather.ProbeTimeout + 5*time.Second):
		t.Fatalf(
			"read of a frozen server still waiting after %s: chat ls would hang",
			gather.ProbeTimeout+5*time.Second,
		)
	}
}

// argvProc is a process table of fixed argv per pid.
type argvProc struct {
	gather.ProcFS
	argv map[int][]string
}

func (proc argvProc) Cmdline(pid int) ([]string, error) {
	argv, found := proc.argv[pid]
	if !found {
		return nil, errors.New("no such process")
	}
	return argv, nil
}

// TestHooklessLaunchReadsTheEnginesSettingsAsTheSpawnAuditDoes pins the
// presence layer's "launched without Herdr hooks" read: known only when the
// pane's Claude argv carries a --settings payload (or none and is not a resume),
// unknown — never guessed — when the launch cannot be read.
func TestHooklessLaunchReadsTheEnginesSettingsAsTheSpawnAuditDoes(t *testing.T) {
	herdrHook := func(event herdr.Event) string {
		return `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":` +
			jsonQuote(herdr.HookCommand("/h/.local/bin/pfm", pfmengine.Claude, event)) + `}]}]}}`
	}
	const usageOnly = `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command",` +
		`"command":"/h/.local/bin/pfm usage-hook"}]}]}}`
	cases := []struct {
		name         string
		pids         []int
		argv         map[int][]string
		wantHookless bool
		wantKnown    bool
	}{
		{
			name: "payload without a Herdr hook", pids: []int{10},
			argv:         map[int][]string{10: {"/opt/claude", "--settings", usageOnly}},
			wantHookless: true, wantKnown: true,
		},
		{
			name: "payload with the Herdr hooks", pids: []int{10},
			argv:      map[int][]string{10: {"/opt/claude", "--settings", herdrHook(herdr.EventIdle)}},
			wantKnown: true,
		},
		{
			name: "file-backed settings are unverified", pids: []int{10},
			argv: map[int][]string{10: {"/opt/claude", "--settings", "/etc/settings.json"}},
		},
		{
			name: "resumed argv with no payload is unverified", pids: []int{10},
			argv: map[int][]string{10: {"/opt/claude", "--resume", "abc"}},
		},
		{
			name: "no Claude process in the pane", pids: []int{10},
			argv: map[int][]string{10: {"/bin/zsh"}},
		},
		{name: "no pane process", pids: nil},
		{
			name: "an unparsable payload is unverified", pids: []int{10},
			argv: map[int][]string{10: {"/opt/claude", "--settings", "{not json"}},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			read := hooklessLaunch(argvProc{argv: test.argv}, "/opt/claude")
			hookless, known := read(herdr.Seat{PanePIDs: test.pids})
			if hookless != test.wantHookless || known != test.wantKnown {
				t.Fatalf(
					"hooklessLaunch = hookless %v known %v, want %v %v",
					hookless, known, test.wantHookless, test.wantKnown,
				)
			}
		})
	}
}

func jsonQuote(text string) string {
	encoded, err := json.Marshal(text)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
