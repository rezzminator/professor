package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// shieldRunner answers `herdr-shield list --json` with listing on stdout, or on
// stderr with a nonzero exit; it stages a herdr-shield on PATH so the binary
// resolves before the fake answers. testjail's stager imports this package,
// so the stand-in is a link to sh, never run: the fake answers every call.
func shieldRunner(t *testing.T, listing string, exit int) *deps.FakeRunner {
	t.Helper()
	sh, err := deps.Resolve("sh")
	if err != nil {
		t.Fatalf("resolve sh for the %s stand-in: %v", ShieldBinary, err)
	}
	dir := t.TempDir()
	if err := os.Symlink(sh, filepath.Join(dir, ShieldBinary)); err != nil {
		t.Fatalf("link %s stand-in in %s to %s: %v", ShieldBinary, dir, sh, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+paths.OSEnv{}.Get("PATH"))
	result := deps.RunResult{Stdout: []byte(listing)}
	if exit != 0 {
		result = deps.RunResult{Stderr: []byte(listing), ExitCode: exit}
	}
	runner := &deps.FakeRunner{}
	runner.Script(nil, result, nil)
	return runner
}

const (
	shieldSocket = "/run/fixture/herdr/herdr.sock"
	shieldRelay  = "/run/fixture/herdr-shield/h1/herdr-api.sock"
)

// shieldProc is a process table: each pid's stat and argv.
type shieldProc map[int]struct {
	stat ProcStat
	argv []string
}

func (proc shieldProc) PIDs() ([]int, error) {
	pids := make([]int, 0, len(proc))
	for pid := range proc {
		pids = append(pids, pid)
	}
	return pids, nil
}

func (proc shieldProc) Stat(pid int) (ProcStat, error) {
	process, found := proc[pid]
	if !found {
		return ProcStat{}, fs.ErrNotExist
	}
	return process.stat, nil
}

func (proc shieldProc) Cmdline(pid int) ([]string, error) {
	process, found := proc[pid]
	if !found {
		return nil, fs.ErrNotExist
	}
	return process.argv, nil
}

// shieldTable is a holder h1 (pid 300, started 30) whose child zsh (pid 310,
// started 31) runs pfm (pid 320) in its terminal's foreground group 320, and
// a background job (pid 330) of its own group.
func shieldTable() shieldProc {
	return shieldProc{
		300: {ProcStat{Start: 30, Group: 300, TerminalGroup: -1}, []string{"herdr-shield", "holder"}},
		310: {ProcStat{Start: 31, Group: 310, TerminalGroup: 320}, []string{"-zsh"}},
		320: {ProcStat{Start: 32, Group: 320, TerminalGroup: 320}, []string{"/opt/fixture/pfm", "chat", "open"}},
		330: {ProcStat{Start: 33, Group: 330, TerminalGroup: 320}, []string{"sleep", "60"}},
	}
}

// shieldListing is `herdr-shield list --json` holding holder h1 bound to w1:p4 on
// shieldSocket in state, w1:p4 listed with status.
func shieldListing(state string, live, orphan bool, status string) string {
	return fmt.Sprintf(`{"enumeration":"OK","error":null,"holders":[{"id":"h1","holder_live":%t,"state":%q,`+
		`"orphan":%t,"holder":{"pid":300,"start":30,"boot":"b"},"child":{"pid":310,"start":31,"boot":"b"},`+
		`"binding":{"pane":"w1:p4","tab":"w1:t4","workspace":"w1","socket":%q}}],`+
		`"panes":[{"pane_id":"w1:p4","status":%q}]}`, live, state, orphan, shieldSocket, status)
}

func processInfo(pane, foreground string) json.RawMessage {
	return json.RawMessage(`{"type":"pane_process_info","process_info":{"pane_id":"` + pane +
		`","shell_pid":200,"foreground_processes":` + foreground + `}}`)
}

const attachClient = `[{"pid":200,"name":"herdr-shield","argv":["/opt/fixture/herdr-shield"]}]`

func TestShieldProcessesSeesTheHoldersShell(t *testing.T) {
	held := PaneProcesses{
		Pane:  "w1:p4",
		Shell: 310,
		Foreground: []Process{
			{PID: 320, Name: "pfm", Argv0: "/opt/fixture/pfm", Argv: []string{"/opt/fixture/pfm", "chat", "open"}},
		},
	}
	attached := PaneProcesses{
		Pane: "w1:p4", Shell: 200,
		Foreground: []Process{{PID: 200, Name: "herdr-shield", Argv: []string{"/opt/fixture/herdr-shield"}}},
	}
	for _, tc := range []struct {
		name, socket, foreground, listing string
		exit                              int
		table                             shieldProc
		want                              PaneProcesses
		holder                            bool
		shieldMissing                     bool
		diagnostic                        string
		// listed is the socket each herdr-shield run was pointed at, in order.
		listed []string
	}{
		{
			name: "unheld pane", socket: shieldSocket, foreground: `[{"pid":200,"name":"zsh","argv":["-zsh"]}]`,
			listing: "not run", exit: 3,
			want: PaneProcesses{Pane: "w1:p4", Shell: 200, Foreground: []Process{{PID: 200, Name: "zsh", Argv: []string{"-zsh"}}}},
		},
		{
			name: "held pane", socket: shieldSocket, foreground: attachClient, listing: shieldListing("running", true, false, "PROTECTED"),
			table: shieldTable(), want: held, holder: true, listed: []string{shieldSocket},
		},
		{
			name: "held pane asked through its relay", socket: shieldRelay, foreground: attachClient,
			listing: shieldListing("running", true, false, "PROTECTED"), table: shieldTable(), want: held, holder: true,
			listed: []string{shieldRelay, shieldSocket},
		},
		{
			name: "recovery screen", socket: shieldSocket, foreground: attachClient,
			listing: shieldListing("exited", true, true, "UNPROTECTED"), table: shieldTable(), want: attached,
			listed: []string{shieldSocket},
		},
		{
			name: "protected without a running holder", socket: shieldSocket, foreground: attachClient,
			listing: shieldListing("exited", true, true, "PROTECTED"), table: shieldTable(), diagnostic: "lists no running holder",
			listed: []string{shieldSocket},
		},
		{
			name: "list fails", socket: shieldSocket, foreground: attachClient, listing: "user manager unavailable", exit: 1,
			diagnostic: "herdr-shield list --json against " + shieldSocket + ": exit 1: user manager unavailable",
			listed:     []string{shieldSocket},
		},
		{
			name: "herdr-shield missing from PATH", socket: shieldSocket, foreground: attachClient,
			listing: shieldListing("running", true, false, "PROTECTED"), shieldMissing: true,
			diagnostic: `resolve herdr-shield: exec: "herdr-shield": executable file not found in $PATH`,
		},
		{
			name: "enumeration failed", socket: shieldSocket, foreground: attachClient,
			listing:    `{"enumeration":"ERROR","error":"pane.list refused","holders":[],"panes":[]}`,
			diagnostic: `enumeration "ERROR": pane.list refused`, listed: []string{shieldSocket},
		},
		{
			name: "exited foreground member", socket: shieldSocket, foreground: attachClient,
			listing: shieldListing("running", true, false, "PROTECTED"),
			table: func() shieldProc {
				table := shieldTable()
				table[340] = struct {
					stat ProcStat
					argv []string
				}{ProcStat{Start: 34, Group: 320, TerminalGroup: 320}, nil}
				return table
			}(),
			want: held, holder: true, listed: []string{shieldSocket},
		},
		{
			name: "reused child pid", socket: shieldSocket, foreground: attachClient,
			listing: shieldListing("running", true, false, "PROTECTED"),
			table: func() shieldProc {
				table := shieldTable()
				table[310] = struct {
					stat ProcStat
					argv []string
				}{ProcStat{Start: 99, Group: 310, TerminalGroup: 320}, []string{"-zsh"}}
				return table
			}(),
			diagnostic: "child pid 310 started at 99, herdr-shield recorded 31", listed: []string{shieldSocket},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// pfm asks from inside a held shell: herdr-shield refuses a run carrying
			// that shell's HERDR_SHIELD_INNER and lists against HERDR_SOCKET_PATH.
			t.Setenv(EnvShieldInner, "h1")
			t.Setenv(EnvSocket, shieldRelay)
			runner := shieldRunner(t, tc.listing, tc.exit)
			if tc.shieldMissing {
				t.Setenv("PATH", t.TempDir())
			}
			got, err := (&Shield{Runner: runner, Proc: tc.table}).Processes(
				context.Background(), tc.socket, processInfo("w1:p4", tc.foreground),
			)
			var listed []string
			for _, call := range runner.Calls() {
				if !reflect.DeepEqual(call.Argv[1:], []string{"list", "--json"}) {
					t.Errorf("ran %q, want herdr-shield list --json", call.Argv)
				}
				for _, entry := range call.Opts.Env {
					name, value, _ := strings.Cut(entry, "=")
					switch name {
					case EnvShieldInner:
						t.Errorf("herdr-shield ran with %s, which it refuses", entry)
					case EnvSocket:
						listed = append(listed, value)
					}
				}
			}
			if !reflect.DeepEqual(listed, tc.listed) {
				t.Errorf("herdr-shield listed against %q, want %q", listed, tc.listed)
			}
			if tc.diagnostic != "" {
				if err == nil || !strings.Contains(err.Error(), tc.diagnostic) {
					t.Fatalf("Processes = %+v, %v; want error %q", got, err, tc.diagnostic)
				}
				return
			}
			if err != nil || (got.Holder != nil) != tc.holder {
				t.Fatalf("Processes = %+v, %v; want holder %t", got, err, tc.holder)
			}
			got.Holder = nil
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Processes = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestShieldUpstreamMapsOnlyARelay(t *testing.T) {
	runner := shieldRunner(t, shieldListing("running", true, false, "PROTECTED"), 0)
	shield := &Shield{Runner: runner}
	for _, tc := range []struct{ socket, pane, wantSocket, wantPane string }{
		{shieldRelay, "w1:p1", shieldSocket, "w1:p4"},
		{shieldSocket, "w1:p1", shieldSocket, "w1:p1"},
	} {
		socket, pane, err := shield.Upstream(context.Background(), tc.socket, tc.pane)
		if err != nil || socket != tc.wantSocket || pane != tc.wantPane {
			t.Errorf("Upstream(%s, %s) = %s, %s, %v; want %s, %s", tc.socket, tc.pane, socket, pane, err,
				tc.wantSocket, tc.wantPane)
		}
	}
	_, _, err := shield.Upstream(context.Background(), "/run/fixture/herdr-shield/h9/herdr-api.sock", "w1:p1")
	if err == nil || !strings.Contains(err.Error(), "herdr-shield lists no holder h9") {
		t.Fatalf("an unlisted relay mapped: %v", err)
	}
	if calls := len(runner.Calls()); calls != 2 {
		t.Fatalf("herdr-shield ran %d times for reads of two relays within its freshness; want 2", calls)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	shield = &Shield{Runner: runner}
	_, _, _ = shield.Upstream(cancelled, shieldRelay, "w1:p1")
	if _, _, err := shield.Upstream(context.Background(), shieldRelay, "w1:p1"); err != nil {
		t.Fatalf("Upstream after a cancelled read: %v", err)
	}
	if calls := len(runner.Calls()); calls != 4 {
		t.Fatalf("herdr-shield ran %d times, want 4: a listing its caller cut short served the next read", calls)
	}
}
