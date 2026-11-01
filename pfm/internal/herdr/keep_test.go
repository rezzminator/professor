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

// keepRunner answers `herdr-keep list --json` with listing on stdout, or on
// stderr with a nonzero exit; it stages a herdr-keep on PATH so the binary
// resolves before the fake answers. testjail's stager imports this package,
// so the stand-in is a link to sh, never run: the fake answers every call.
func keepRunner(t *testing.T, listing string, exit int) *deps.FakeRunner {
	t.Helper()
	sh, err := deps.Resolve("sh")
	if err != nil {
		t.Fatalf("resolve sh for the %s stand-in: %v", KeepBinary, err)
	}
	dir := t.TempDir()
	if err := os.Symlink(sh, filepath.Join(dir, KeepBinary)); err != nil {
		t.Fatalf("link %s stand-in in %s to %s: %v", KeepBinary, dir, sh, err)
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
	keepSocket = "/run/fixture/herdr/herdr.sock"
	keepRelay  = "/run/fixture/herdr-keep/h1/herdr-api.sock"
)

// keepProc is a process table: each pid's stat and argv.
type keepProc map[int]struct {
	stat ProcStat
	argv []string
}

func (proc keepProc) PIDs() ([]int, error) {
	pids := make([]int, 0, len(proc))
	for pid := range proc {
		pids = append(pids, pid)
	}
	return pids, nil
}

func (proc keepProc) Stat(pid int) (ProcStat, error) {
	process, found := proc[pid]
	if !found {
		return ProcStat{}, fs.ErrNotExist
	}
	return process.stat, nil
}

func (proc keepProc) Cmdline(pid int) ([]string, error) {
	process, found := proc[pid]
	if !found {
		return nil, fs.ErrNotExist
	}
	return process.argv, nil
}

// keepTable is a holder h1 (pid 300, started 30) whose child zsh (pid 310,
// started 31) runs pfm (pid 320) in its terminal's foreground group 320, and
// a background job (pid 330) of its own group.
func keepTable() keepProc {
	return keepProc{
		300: {ProcStat{Start: 30, Group: 300, TerminalGroup: -1}, []string{"herdr-keep", "holder"}},
		310: {ProcStat{Start: 31, Group: 310, TerminalGroup: 320}, []string{"-zsh"}},
		320: {ProcStat{Start: 32, Group: 320, TerminalGroup: 320}, []string{"/opt/fixture/pfm", "chat", "open"}},
		330: {ProcStat{Start: 33, Group: 330, TerminalGroup: 320}, []string{"sleep", "60"}},
	}
}

// keepListing is `herdr-keep list --json` holding holder h1 bound to w1:p4 on
// keepSocket in state, w1:p4 listed with status.
func keepListing(state string, live, orphan bool, status string) string {
	return fmt.Sprintf(`{"enumeration":"OK","error":null,"holders":[{"id":"h1","holder_live":%t,"state":%q,`+
		`"orphan":%t,"holder":{"pid":300,"start":30,"boot":"b"},"child":{"pid":310,"start":31,"boot":"b"},`+
		`"binding":{"pane":"w1:p4","tab":"w1:t4","workspace":"w1","socket":%q}}],`+
		`"panes":[{"pane_id":"w1:p4","status":%q}]}`, live, state, orphan, keepSocket, status)
}

func processInfo(pane, foreground string) json.RawMessage {
	return json.RawMessage(`{"type":"pane_process_info","process_info":{"pane_id":"` + pane +
		`","shell_pid":200,"foreground_processes":` + foreground + `}}`)
}

const attachClient = `[{"pid":200,"name":"herdr-keep","argv":["/opt/fixture/herdr-keep"]}]`

func TestKeepProcessesSeesTheHoldersShell(t *testing.T) {
	held := PaneProcesses{
		Pane:  "w1:p4",
		Shell: 310,
		Foreground: []Process{
			{PID: 320, Name: "pfm", Argv0: "/opt/fixture/pfm", Argv: []string{"/opt/fixture/pfm", "chat", "open"}},
		},
	}
	attached := PaneProcesses{
		Pane: "w1:p4", Shell: 200,
		Foreground: []Process{{PID: 200, Name: "herdr-keep", Argv: []string{"/opt/fixture/herdr-keep"}}},
	}
	for _, tc := range []struct {
		name, socket, foreground, listing string
		exit                              int
		table                             keepProc
		want                              PaneProcesses
		holder                            bool
		keepMissing                       bool
		diagnostic                        string
		// listed is the socket each herdr-keep run was pointed at, in order.
		listed []string
	}{
		{
			name: "unheld pane", socket: keepSocket, foreground: `[{"pid":200,"name":"zsh","argv":["-zsh"]}]`,
			listing: "not run", exit: 3,
			want: PaneProcesses{Pane: "w1:p4", Shell: 200, Foreground: []Process{{PID: 200, Name: "zsh", Argv: []string{"-zsh"}}}},
		},
		{
			name: "held pane", socket: keepSocket, foreground: attachClient, listing: keepListing("running", true, false, "PROTECTED"),
			table: keepTable(), want: held, holder: true, listed: []string{keepSocket},
		},
		{
			name: "held pane asked through its relay", socket: keepRelay, foreground: attachClient,
			listing: keepListing("running", true, false, "PROTECTED"), table: keepTable(), want: held, holder: true,
			listed: []string{keepRelay, keepSocket},
		},
		{
			name: "recovery screen", socket: keepSocket, foreground: attachClient,
			listing: keepListing("exited", true, true, "UNPROTECTED"), table: keepTable(), want: attached,
			listed: []string{keepSocket},
		},
		{
			name: "protected without a running holder", socket: keepSocket, foreground: attachClient,
			listing: keepListing("exited", true, true, "PROTECTED"), table: keepTable(), diagnostic: "lists no running holder",
			listed: []string{keepSocket},
		},
		{
			name: "list fails", socket: keepSocket, foreground: attachClient, listing: "user manager unavailable", exit: 1,
			diagnostic: "herdr-keep list --json against " + keepSocket + ": exit 1: user manager unavailable",
			listed:     []string{keepSocket},
		},
		{
			name: "herdr-keep missing from PATH", socket: keepSocket, foreground: attachClient,
			listing: keepListing("running", true, false, "PROTECTED"), keepMissing: true,
			diagnostic: `resolve herdr-keep: exec: "herdr-keep": executable file not found in $PATH`,
		},
		{
			name: "enumeration failed", socket: keepSocket, foreground: attachClient,
			listing:    `{"enumeration":"ERROR","error":"pane.list refused","holders":[],"panes":[]}`,
			diagnostic: `enumeration "ERROR": pane.list refused`, listed: []string{keepSocket},
		},
		{
			name: "exited foreground member", socket: keepSocket, foreground: attachClient,
			listing: keepListing("running", true, false, "PROTECTED"),
			table: func() keepProc {
				table := keepTable()
				table[340] = struct {
					stat ProcStat
					argv []string
				}{ProcStat{Start: 34, Group: 320, TerminalGroup: 320}, nil}
				return table
			}(),
			want: held, holder: true, listed: []string{keepSocket},
		},
		{
			// Between an engine's exit and its relaunch the terminal's foreground
			// group has no member left: Herdr reports a plain pane so as no
			// foreground, never as a failed read.
			name: "foreground group emptied by its exit", socket: keepSocket, foreground: attachClient,
			listing: keepListing("running", true, false, "PROTECTED"),
			table: func() keepProc {
				table := keepTable()
				delete(table, 320)
				return table
			}(),
			want: PaneProcesses{Pane: "w1:p4", Shell: 310, Foreground: []Process{}}, holder: true, listed: []string{keepSocket},
		},
		{
			name: "reused child pid", socket: keepSocket, foreground: attachClient,
			listing: keepListing("running", true, false, "PROTECTED"),
			table: func() keepProc {
				table := keepTable()
				table[310] = struct {
					stat ProcStat
					argv []string
				}{ProcStat{Start: 99, Group: 310, TerminalGroup: 320}, []string{"-zsh"}}
				return table
			}(),
			diagnostic: "child pid 310 started at 99, herdr-keep recorded 31", listed: []string{keepSocket},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// pfm asks from inside a held shell: herdr-keep refuses a run carrying
			// that shell's HERDR_KEEP_INNER and lists against HERDR_SOCKET_PATH.
			t.Setenv(EnvKeepInner, "h1")
			t.Setenv(EnvSocket, keepRelay)
			runner := keepRunner(t, tc.listing, tc.exit)
			if tc.keepMissing {
				t.Setenv("PATH", t.TempDir())
			}
			got, err := (&Keep{Runner: runner, Proc: tc.table}).Processes(
				context.Background(), tc.socket, processInfo("w1:p4", tc.foreground),
			)
			var listed []string
			for _, call := range runner.Calls() {
				if !reflect.DeepEqual(call.Argv[1:], []string{"list", "--json"}) {
					t.Errorf("ran %q, want herdr-keep list --json", call.Argv)
				}
				for _, entry := range call.Opts.Env {
					name, value, _ := strings.Cut(entry, "=")
					switch name {
					case EnvKeepInner:
						t.Errorf("herdr-keep ran with %s, which it refuses", entry)
					case EnvSocket:
						listed = append(listed, value)
					}
				}
			}
			if !reflect.DeepEqual(listed, tc.listed) {
				t.Errorf("herdr-keep listed against %q, want %q", listed, tc.listed)
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

func TestKeepUpstreamMapsOnlyARelay(t *testing.T) {
	runner := keepRunner(t, keepListing("running", true, false, "PROTECTED"), 0)
	keep := &Keep{Runner: runner}
	for _, tc := range []struct{ socket, pane, wantSocket, wantPane string }{
		{keepRelay, "w1:p1", keepSocket, "w1:p4"},
		{keepSocket, "w1:p1", keepSocket, "w1:p1"},
	} {
		socket, pane, err := keep.Upstream(context.Background(), tc.socket, tc.pane)
		if err != nil || socket != tc.wantSocket || pane != tc.wantPane {
			t.Errorf("Upstream(%s, %s) = %s, %s, %v; want %s, %s", tc.socket, tc.pane, socket, pane, err,
				tc.wantSocket, tc.wantPane)
		}
	}
	_, _, err := keep.Upstream(context.Background(), "/run/fixture/herdr-keep/h9/herdr-api.sock", "w1:p1")
	if err == nil || !strings.Contains(err.Error(), "herdr-keep lists no holder h9") {
		t.Fatalf("an unlisted relay mapped: %v", err)
	}
	if calls := len(runner.Calls()); calls != 2 {
		t.Fatalf("herdr-keep ran %d times for reads of two relays within its freshness; want 2", calls)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	keep = &Keep{Runner: runner}
	_, _, _ = keep.Upstream(cancelled, keepRelay, "w1:p1")
	if _, _, err := keep.Upstream(context.Background(), keepRelay, "w1:p1"); err != nil {
		t.Fatalf("Upstream after a cancelled read: %v", err)
	}
	if calls := len(runner.Calls()); calls != 4 {
		t.Fatalf("herdr-keep ran %d times, want 4: a listing its caller cut short served the next read", calls)
	}
}
