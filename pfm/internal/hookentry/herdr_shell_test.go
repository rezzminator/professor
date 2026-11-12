package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// A nested shell left by Ctrl-D releases from its exit hook while its parent
// shell, back at its prompt, reports the nested shell's own run as done: two
// shells, two locks, one pane, and a title that carries no seq. A release
// started before the latest done on its pane leaves that done's title up; the
// parent's next command still clears it.
func TestHerdrShellAnExitedNestedShellNeverClearsALaterDoneTitle(t *testing.T) {
	const untitle = `pane.report_metadata {"pane_id":"wT:p1","source":"pfm-shell","clear_title":true}`
	server := newFakeHerdr(t)
	server.addShellPane("wT:p1", "t1", 7, 4242)
	jail := newHerdrJail(t)
	clock := int64(1_000_000)
	deps := jail.deps(herdrPaneEnv(server, "wT:p1"), &clock)
	run := func(args ...string) []string {
		t.Helper()
		before := len(server.calls())
		var stderr bytes.Buffer
		if rc := reportShellRun(args, &stderr, deps); rc != 0 || stderr.Len() != 0 {
			t.Fatalf("%q: rc=%d stderr=%q, want a quiet run", args, rc, stderr.String())
		}
		return server.calls()[before:]
	}
	run("done", "--pid", "4242", "--at", "1760000010.5", "--command", "sleep 12", "--exit", "0", "--seconds", "12")
	const parentTitle = `pane.report_metadata {"pane_id":"wT:p1","source":"pfm-shell",` +
		`"title":"zsh -i exit 0 · 19s"}`
	parent := run("done", "--pid", "7", "--at", "1760000012.5", "--command", "zsh -i", "--exit", "0", "--seconds", "19")
	if !slices.Contains(parent, parentTitle) {
		t.Fatalf("parent done calls = %q, want %q among them", parent, parentTitle)
	}
	// The nested shell's own last done, started before the parent's, reaching
	// the pane lock after it: it reports nothing over the later title.
	late := run("done", "--pid", "4242", "--at", "1760000011", "--command", "make", "--exit", "0", "--seconds", "13")
	for _, call := range late {
		if strings.HasPrefix(call, "pane.report_") {
			t.Fatalf("an older done's calls = %q, want no report under the parent's later done", late)
		}
	}
	server.foreground("wT:p1")
	if got := run("release", "--pid", "4242", "--at", "1760000012.1"); slices.Contains(got, untitle) {
		t.Fatalf("exited nested shell's release calls = %q, want no %q under the parent's later done", got, untitle)
	}
	if got := run("release", "--pid", "7", "--at", "1760000020.5"); !slices.Contains(got, untitle) {
		t.Fatalf("parent's next command release calls = %q, want %q among them", got, untitle)
	}
}

// A pane's title record that holds no time (a write cut short, or a value no
// shell clock gives) never stops the pane's reports for good: the run reports
// as if no done held the title, its error row names the record, and the record
// is rewritten or dropped, so the next run is quiet.
func TestHerdrShellADamagedTitleRecordNeverStopsThePane(t *testing.T) {
	const (
		title   = `pane.report_metadata {"pane_id":"wT:p1","source":"pfm-shell","title":"make exit 0 · 12s"}`
		untitle = `pane.report_metadata {"pane_id":"wT:p1","source":"pfm-shell","clear_title":true}`
	)
	done := []string{
		"done", "--pid", "4242", "--at", "1760000010.5", "--command", "make", "--exit", "0", "--seconds", "12",
	}
	release := []string{"release", "--pid", "4242", "--at", "1760000020.5"}
	for _, record := range []string{"", "garbage", "Inf", "NaN"} {
		for name, test := range map[string]struct {
			args []string
			want string
		}{"done": {done, title}, "release": {release, untitle}} {
			t.Run(fmt.Sprintf("%s over %q", name, record), func(t *testing.T) {
				server := newFakeHerdr(t)
				server.addShellPane("wT:p1", "t1", 4242)
				jail := newHerdrJail(t)
				clock := int64(1_000_000)
				deps := jail.deps(herdrPaneEnv(server, "wT:p1"), &clock)
				if err := os.MkdirAll(jail.lockDir, 0o700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(jail.lockDir, "pfm-shell-pane-wT:p1.title")
				if err := os.WriteFile(path, []byte(record), 0o600); err != nil {
					t.Fatal(err)
				}
				var stderr bytes.Buffer
				if rc := reportShellRun(test.args, &stderr, deps); rc != 0 {
					t.Fatalf("rc=%d stderr=%q, want 0", rc, stderr.String())
				}
				if got := server.calls(); !slices.Contains(got, test.want) {
					t.Fatalf("calls over a damaged record = %q, want %q among them", got, test.want)
				}
				if logged := jail.logged(herdrShellLog); !strings.Contains(logged, path) {
					t.Fatalf("error log = %q, want a row naming %s", logged, path)
				}
				stderr.Reset()
				if rc := reportShellRun(release, &stderr, deps); rc != 0 || stderr.Len() != 0 {
					t.Fatalf("next run: rc=%d stderr=%q, want a quiet run", rc, stderr.String())
				}
			})
		}
	}
}

// A done whose send errors may still have reached Herdr (a timed-out answer),
// so it holds the pane's title like one Herdr took: an older release, here the
// exited nested shell's, never clears a title that may be showing.
func TestHerdrShellADoneWhoseSendErrorsStillHoldsThePaneTitle(t *testing.T) {
	const untitle = `pane.report_metadata {"pane_id":"wT:p1","source":"pfm-shell","clear_title":true}`
	server := newFakeHerdr(t)
	server.addShellPane("wT:p1", "t1", 7, 4242)
	jail := newHerdrJail(t)
	clock := int64(1_000_000)
	deps := jail.deps(herdrPaneEnv(server, "wT:p1"), &clock)
	var stderr bytes.Buffer
	run := func(args ...string) []string {
		t.Helper()
		before := len(server.calls())
		stderr.Reset()
		if rc := reportShellRun(args, &stderr, deps); rc != 0 {
			t.Fatalf("%q: rc=%d stderr=%q, want 0", args, rc, stderr.String())
		}
		return server.calls()[before:]
	}
	run("done", "--pid", "4242", "--at", "1760000010.5", "--command", "sleep 12", "--exit", "0", "--seconds", "12")
	server.refuse["pane.report_metadata"] = "internal"
	run("done", "--pid", "7", "--at", "1760000012.5", "--command", "zsh -i", "--exit", "0", "--seconds", "19")
	if !strings.Contains(stderr.String(), "pane.report_metadata") {
		t.Fatalf("parent done stderr = %q, want the refused title named", stderr.String())
	}
	delete(server.refuse, "pane.report_metadata")
	server.foreground("wT:p1")
	if got := run("release", "--pid", "4242", "--at", "1760000012.1"); slices.Contains(got, untitle) {
		t.Fatalf("an older release after a done whose send errored = %q, want no %q", got, untitle)
	}
}

func TestHerdrShellMovedAliasUsesCanonicalIdentity(t *testing.T) {
	server := newFakeHerdr(t)
	server.addShellPane("wT:p1", "t1", 7, 4242)
	server.move("wT:p1", "wU:p3")
	server.aliases = map[string]string{"wT:p1": "wU:p3"}
	jail := newHerdrJail(t)
	clock := int64(1_000_000)
	deps := jail.deps(herdrPaneEnv(server, "wT:p1"), &clock)
	for _, step := range []struct {
		args       []string
		foreground int
		wantTitle  string
	}{
		{
			[]string{"done", "--pid", "4242", "--at", "100", "--command", "make test", "--exit", "0", "--seconds", "12"},
			4242, "make test exit 0 · 12s",
		},
		{[]string{"release", "--pid", "4242", "--at", "101"}, 5000, ""},
	} {
		server.foreground("wU:p3", step.foreground)
		var stderr bytes.Buffer
		if rc := reportShellRun(step.args, &stderr, deps); rc != 0 || stderr.Len() != 0 {
			t.Fatalf("%q: rc=%d stderr=%q", step.args, rc, stderr.String())
		}
		var lastTitle struct {
			Params struct {
				Pane  string `json:"pane_id"`
				Title string
			}
		}
		for _, line := range server.requests() {
			if strings.Contains(line, `"method":"pane.report_metadata"`) {
				lastTitle.Params.Title = ""
				if err := json.Unmarshal([]byte(line), &lastTitle); err != nil {
					t.Fatal(err)
				}
			}
		}
		if lastTitle.Params.Pane != "wU:p3" || lastTitle.Params.Title != step.wantTitle {
			t.Fatalf("%q: title pane=%q text=%q, want canonical wU:p3 and %q",
				step.args, lastTitle.Params.Pane, lastTitle.Params.Title, step.wantTitle)
		}
		if step.foreground == 4242 {
			data, err := os.ReadFile(filepath.Join(jail.lockDir, "pfm-shell-4242.pane"))
			if err != nil {
				t.Fatalf("canonical terminal record: %v", err)
			}
			if !strings.Contains(string(data), `"PaneID":"wU:p3"`) ||
				!strings.Contains(string(data), `"TerminalID":"t1"`) {
				t.Fatalf("terminal record=%s, want canonical pane wU:p3 and terminal t1", data)
			}
			if _, err := os.Stat(shellTitlePath(jail.lockDir, "wU:p3")); err != nil {
				t.Fatalf("canonical title record: %v", err)
			}
		}
	}
	server.move("wU:p3", "wV:p5")
	err := recordShellPane(context.Background(), server.path,
		filepath.Join(jail.lockDir, "pfm-shell-4242.pane"), "wU:p3", func() uint64 { return 300 })
	if err == nil || !strings.Contains(err.Error(), "no longer contains the canonical pane") {
		t.Fatalf("record after a second move: %v, want a visible canonical-pane race error", err)
	}
}
