package hookentry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// A plain shell's long command reports to the Herdr pane whose shell (or one
// of whose foreground processes) is the shell's pid, found by that identity
// because a pane move changes the pane id: working, then idle with the
// command, its exit status and its duration, under the source pfm-shell and the
// agent shell, and the same text as the pane's title, the one report Herdr
// keeps once the bare shell holds the foreground again; release clears both. A failure is an
// error row in the pfm-shell log pfm doctor reads; a release that finds no pane
// running the shell has nothing left to release.
func TestHerdrShellReportsToThePaneRunningTheShell(t *testing.T) {
	const (
		working = `pane.report_agent {"pane_id":"%s","source":"pfm-shell","agent":"shell","state":"working"}`
		idle    = `pane.report_agent {"pane_id":"%s","source":"pfm-shell","agent":"shell","state":"idle","message":"%s"}`
		title   = `pane.report_metadata {"pane_id":"%s","source":"pfm-shell","title":"%s"}`
		release = `pane.release_agent {"pane_id":"%s","source":"pfm-shell","agent":"shell"}`
		untitle = `pane.report_metadata {"pane_id":"%s","source":"pfm-shell","clear_title":true}`
		info    = `pane.process_info {"pane_id":"%s"}`
		listing = `pane.list {}`
	)
	sprintf := fmt.Sprintf
	for name, test := range map[string]struct {
		setup    func(server *fakeHerdr)
		env      map[string]string
		args     []string
		want     []string
		errorLog string
		// hidden is text the user typed that no error line may carry.
		hidden string
		rc     int
	}{
		"done reports working then idle to the pane the shell was born in": {
			setup: func(server *fakeHerdr) { server.addShellPane("wT:p1", "t1", 4242) },
			args:  []string{"done", "--pid", "4242", "--command", "make test", "--exit", "3", "--seconds", "12"},
			want: []string{
				sprintf(info, "wT:p1"), sprintf(working, "wT:p1"), sprintf(idle, "wT:p1", "make test exit 3 · 12s"),
				sprintf(title, "wT:p1", "make test exit 3 · 12s"),
			},
		},
		"a multi-line command reports on one line": {
			setup: func(server *fakeHerdr) { server.addShellPane("wT:p1", "t1", 4242) },
			args: []string{
				"done", "--pid", "4242", "--command", "for i in 1 2\ndo sleep 6\ndone", "--exit", "0", "--seconds", "12",
			},
			want: []string{
				sprintf(info, "wT:p1"), sprintf(working, "wT:p1"),
				sprintf(idle, "wT:p1", "for i in 1 2 do sleep 6 done exit 0 · 12s"),
				sprintf(title, "wT:p1", "for i in 1 2 do sleep 6 done exit 0 · 12s"),
			},
		},
		"a command too long for one message keeps its exit status and duration": {
			setup: func(server *fakeHerdr) { server.addShellPane("wT:p1", "t1", 4242) },
			args: []string{
				"done", "--pid", "4242", "--command", strings.Repeat("x", 300), "--exit", "3", "--seconds", "12",
			},
			want: []string{
				sprintf(info, "wT:p1"), sprintf(working, "wT:p1"),
				sprintf(idle, "wT:p1", strings.Repeat("x", 226)+"… exit 3 · 12s"),
				sprintf(title, "wT:p1", strings.Repeat("x", 66)+"… exit 3 · 12s"),
			},
		},
		"a pane moved away is found by its shell": {
			setup: func(server *fakeHerdr) {
				server.addShellPane("wT:p1", "t1", 4242)
				server.addShellPane("wV:p2", "t2", 7)
				server.move("wT:p1", "wU:p3")
			},
			args: []string{"done", "--pid", "4242", "--command", "make test", "--exit", "3", "--seconds", "12"},
			want: []string{
				sprintf(info, "wT:p1"), listing, sprintf(info, "wU:p3"),
				sprintf(working, "wU:p3"), sprintf(idle, "wU:p3", "make test exit 3 · 12s"),
				sprintf(title, "wU:p3", "make test exit 3 · 12s"),
			},
		},
		"a pane id another shell now holds is skipped": {
			setup: func(server *fakeHerdr) {
				server.addShellPane("wT:p1", "t9", 7)
				server.addShellPane("wU:p3", "t1", 4242)
			},
			args: []string{"release", "--pid", "4242"},
			want: []string{sprintf(info, "wT:p1"), listing, sprintf(info, "wU:p3"), sprintf(release, "wU:p3"), sprintf(untitle, "wU:p3")},
		},
		"a nested shell is found among the foreground processes": {
			setup: func(server *fakeHerdr) { server.addShellPane("wT:p1", "t1", 7, 4242) },
			args:  []string{"release", "--pid", "4242"},
			want:  []string{sprintf(info, "wT:p1"), sprintf(release, "wT:p1"), sprintf(untitle, "wT:p1")},
		},
		"a release with no pane running the shell sends nothing": {
			setup: func(server *fakeHerdr) { server.addShellPane("wT:p1", "t1", 7) },
			args:  []string{"release", "--pid", "4242"},
			want:  []string{sprintf(info, "wT:p1"), listing},
		},
		"a done with no pane running the shell is an error row": {
			setup:    func(server *fakeHerdr) { server.addShellPane("wT:p1", "t1", 7) },
			args:     []string{"done", "--pid", "4242", "--command", "make test", "--exit", "3", "--seconds", "12"},
			want:     []string{sprintf(info, "wT:p1"), listing},
			errorLog: "pfm internal herdr-shell done (shell 4242): no Herdr pane runs shell 4242",
		},
		"a refused report is an error row": {
			setup: func(server *fakeHerdr) {
				server.addShellPane("wT:p1", "t1", 4242)
				server.refuse["pane.report_agent"] = "invalid_state"
			},
			args:     []string{"done", "--pid", "4242", "--command", "make test", "--exit", "3", "--seconds", "12"},
			want:     []string{sprintf(info, "wT:p1"), sprintf(working, "wT:p1")},
			errorLog: "invalid_state",
		},
		"a refused title is an error row that never carries the command": {
			setup: func(server *fakeHerdr) {
				server.addShellPane("wT:p1", "t1", 4242)
				server.refuse["pane.report_metadata"] = "invalid_params"
			},
			args: []string{
				"done", "--pid", "4242", "--command", "psql postgres://u:SECRET@db", "--exit", "0", "--seconds", "12",
			},
			want: []string{
				sprintf(info, "wT:p1"), sprintf(working, "wT:p1"),
				sprintf(idle, "wT:p1", "psql postgres://u:SECRET@db exit 0 · 12s"),
				sprintf(title, "wT:p1", "psql postgres://u:SECRET@db exit 0 · 12s"),
			},
			errorLog: "pane.report_metadata",
			hidden:   "SECRET",
		},
		"a release whose title clear Herdr refuses is an error row": {
			setup: func(server *fakeHerdr) {
				server.addShellPane("wT:p1", "t1", 4242)
				server.refuse["pane.report_metadata"] = "internal"
			},
			args:     []string{"release", "--pid", "4242"},
			want:     []string{sprintf(info, "wT:p1"), sprintf(release, "wT:p1"), sprintf(untitle, "wT:p1")},
			errorLog: "pane.report_metadata",
		},
		"a refused report logs the request, never the command it carried": {
			setup: func(server *fakeHerdr) {
				server.addShellPane("wT:p1", "t1", 4242)
				server.refuse["pane.report_agent idle"] = "invalid_state"
			},
			args: []string{
				"done", "--pid", "4242", "--command", "psql postgres://u:SECRET@db", "--exit", "0", "--seconds", "12",
			},
			want: []string{
				sprintf(info, "wT:p1"), sprintf(working, "wT:p1"),
				sprintf(idle, "wT:p1", "psql postgres://u:SECRET@db exit 0 · 12s"),
			},
			errorLog: "pane.report_agent",
			hidden:   "SECRET",
		},
		"a failed pane list is an error row": {
			setup: func(server *fakeHerdr) {
				server.addShellPane("wT:p1", "t1", 7)
				server.refuse["pane.list"] = "internal"
			},
			args:     []string{"release", "--pid", "4242"},
			want:     []string{sprintf(info, "wT:p1"), listing},
			errorLog: "pane.list",
		},
		"no Herdr socket is an error row": {
			env:      map[string]string{"HERDR_SOCKET_PATH": ""},
			args:     []string{"release", "--pid", "4242"},
			errorLog: "pfm internal herdr-shell release (shell 4242): no HERDR_SOCKET_PATH",
		},
		"outside Herdr sends nothing": {
			env:  map[string]string{"HERDR_ENV": ""},
			args: []string{"done", "--pid", "4242", "--command", "make test", "--exit", "3", "--seconds", "12"},
		},
		"an unknown verb is a usage error": {
			args:     []string{"start", "--pid", "4242"},
			rc:       2,
			errorLog: `pfm internal herdr-shell: verb "start", want done or release`,
		},
		"a done without its command is a usage error": {
			args:     []string{"done", "--pid", "4242", "--exit", "3", "--seconds", "12"},
			rc:       2,
			errorLog: "pfm internal herdr-shell done: --pid, --command, --exit and --seconds are all required",
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := newFakeHerdr(t)
			if test.setup != nil {
				test.setup(server)
			}
			env := herdrPaneEnv(server, "wT:p1")
			for key, value := range test.env {
				env[key] = value
			}
			jail := newHerdrJail(t)
			clock := int64(1_000_000)
			deps := jail.deps(env, &clock)
			var stderr bytes.Buffer
			if rc := reportShellRun(test.args, &stderr, deps); rc != test.rc {
				t.Fatalf("rc=%d, want %d (stderr %q)", rc, test.rc, stderr.String())
			}
			if got := server.calls(); strings.Join(got, "\n") != strings.Join(test.want, "\n") {
				t.Fatalf("herdr calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(test.want, "\n"))
			}
			logged := jail.logged("pfm-shell")
			if test.errorLog == "" && logged != "" || !strings.Contains(logged, test.errorLog) {
				t.Fatalf("pfm-shell error log=%q, want %q", logged, test.errorLog)
			}
			if test.hidden != "" && strings.Contains(logged+stderr.String(), test.hidden) {
				t.Fatalf("error log=%q stderr=%q carry %q the user typed", logged, stderr.String(), test.hidden)
			}
			var last uint64
			for _, line := range server.requests() {
				var request struct {
					Method string `json:"method"`
					Params struct {
						Seq uint64 `json:"seq"`
					} `json:"params"`
				}
				if err := json.Unmarshal([]byte(line), &request); err != nil {
					t.Fatalf("undecodable request %q: %v", line, err)
				}
				// A title carries no seq: the shell's lock orders a done before
				// the release that clears it.
				if strings.HasPrefix(request.Method, "pane.re") && request.Method != "pane.report_metadata" {
					if request.Params.Seq <= last {
						t.Fatalf(
							"seq %d after %d: Herdr ignores a report whose seq is not above the last",
							request.Params.Seq,
							last,
						)
					}
					last = request.Params.Seq
				}
			}
		})
	}
}

// A done and the next command's release are two background runs the shell
// starts in order and nothing else orders: a done that runs after the release
// its shell started later reports nothing, so the pane never keeps a finished
// run the next command already cleared; a done from a later command reports.
func TestHerdrShellDoneNeverOutlivesALaterRelease(t *testing.T) {
	server := newFakeHerdr(t)
	server.addShellPane("wT:p1", "t1", 4242)
	jail := newHerdrJail(t)
	clock := int64(1_000_000)
	deps := jail.deps(herdrPaneEnv(server, "wT:p1"), &clock)
	done := func(at string) []string {
		return []string{
			"done", "--pid", "4242", "--at", at, "--command", "make test", "--exit", "3", "--seconds", "12",
		}
	}
	for _, step := range []struct {
		args    []string
		reports int
	}{
		{[]string{"release", "--pid", "4242", "--at", "1760000020.000002"}, 2},
		{done("1760000020.000001"), 0},
		{done("1760000030.5"), 3},
	} {
		before := len(server.calls())
		var stderr bytes.Buffer
		if rc := reportShellRun(step.args, &stderr, deps); rc != 0 || stderr.Len() != 0 {
			t.Fatalf("%q: rc=%d stderr=%q, want a quiet run", step.args, rc, stderr.String())
		}
		reports := 0
		for _, call := range server.calls()[before:] {
			if strings.HasPrefix(call, "pane.re") {
				reports++
			}
		}
		if reports != step.reports {
			t.Fatalf("%q: %d report or release requests (%q), want %d", step.args, reports,
				server.calls()[before:], step.reports)
		}
	}
}

// A nested shell (a zsh started inside a pane's own shell) is found by its
// pane's foreground at done; when its next command starts, that command holds
// the foreground and the nested shell is in no pane's process info. Its
// release goes to the pane its done went to, followed by terminal id, so the
// finished run never outlives the next command, a moved pane included.
func TestHerdrShellReleasesANestedShellAtThePaneItsDoneWentTo(t *testing.T) {
	server := newFakeHerdr(t)
	server.addShellPane("wT:p1", "t1", 7, 4242)
	jail := newHerdrJail(t)
	clock := int64(1_000_000)
	deps := jail.deps(herdrPaneEnv(server, "wT:p1"), &clock)
	var stderr bytes.Buffer
	done := []string{"done", "--pid", "4242", "--command", "make test", "--exit", "0", "--seconds", "12"}
	if rc := reportShellRun(done, &stderr, deps); rc != 0 || stderr.Len() != 0 {
		t.Fatalf("done: rc=%d stderr=%q, want a quiet run", rc, stderr.String())
	}
	server.foreground("wT:p1", 9999)
	server.move("wT:p1", "wU:p5")
	// A refused release is an error row and keeps the record, so the next
	// release still reaches the pane.
	server.refuse["pane.release_agent"] = "internal"
	if rc := reportShellRun([]string{"release", "--pid", "4242"}, &stderr, deps); rc != 0 ||
		!strings.Contains(stderr.String(), "internal") {
		t.Fatalf("refused release: rc=%d stderr=%q, want the refusal named", rc, stderr.String())
	}
	delete(server.refuse, "pane.release_agent")
	stderr.Reset()
	before := len(server.calls())
	if rc := reportShellRun([]string{"release", "--pid", "4242"}, &stderr, deps); rc != 0 || stderr.Len() != 0 {
		t.Fatalf("release: rc=%d stderr=%q, want a quiet run", rc, stderr.String())
	}
	want := `pane.release_agent {"pane_id":"wU:p5","source":"pfm-shell","agent":"shell"}`
	if got := server.calls()[before:]; !slices.Contains(got, want) {
		t.Fatalf("release calls after a refused one = %q, want %q among them", got, want)
	}
}
