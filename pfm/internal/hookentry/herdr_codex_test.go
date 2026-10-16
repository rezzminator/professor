package hookentry

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// codexTUI is pid 90, a Codex TUI the user started from the pane's zsh (pid
// 80); every hook runs as pid 100, a shell Codex started.
const codexTUI = 90

// codexRig is a chat rig whose engine-fired events come from Codex: the hook
// environment is the rig's stale one (a long gone tmux server and Herdr pane
// w9:p9), the TUI's own environment names Herdr pane w1:p1.
func codexRig(t *testing.T) *chatRig {
	t.Helper()
	rig := newChatRig(t)
	rig.jail.procs = map[int]fakeProc{100: {codexTUI, "sh"}, codexTUI: {80, "codex"}, 80: {1, "-zsh"}}
	rig.jail.argv = map[int][]string{codexTUI: {"codex", "-a", "on-request", "-s", "read-only"}}
	rig.jail.paneEnv = map[int]map[string]string{codexTUI: herdrPaneEnv(rig.herdr, "w1:p1")}
	return rig
}

func (rig *chatRig) codex(event, session string, extra ...string) herdrRun {
	rig.t.Helper()
	payload := `{"session_id":"` + session + `","transcript_path":"/h/.codex/sessions/r.jsonl"` +
		strings.Join(extra, "") + `}`
	return rig.run([]string{"--engine", "codex", event}, payload)
}

func codexReportCall(pane, state, session, message string) string {
	if message != "" {
		message = `,"message":"` + message + `"`
	}
	return `pane.report_agent {"pane_id":"` + pane + `","source":"pfm","agent":"codex","state":"` + state + `"` +
		message + `,"agent_session_id":"` + session + `","resume_argv":["pfm","chat","open","` + session + `"]}`
}

// A Codex event reports only to the pane registered for its payload
// session_id: a SessionStart whose hook runs under an interactive Codex TUI
// registers the pane named by that TUI's own environment, never the hook's.
// Anything else (the shared app-server daemon's hooks, another thread's
// events, a session whose TUI is gone) reports nothing and logs one line.
func TestHerdrCodexReportsOnlyToTheRegisteredPane(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(rig *chatRig)
		run   func(rig *chatRig) []herdrRun
		calls []string
		// logs is what the no-session error log must name; empty wants none.
		logs []string
		// records are the session ids left registered.
		records []string
	}{
		{
			name: "a pane-fired SessionStart registers the TUI's pane, and Esc's Interrupt leaves blocked there",
			run: func(rig *chatRig) []herdrRun {
				return []herdrRun{
					rig.codex("start", "x1", `,"source":"startup"`),
					rig.codex("blocked", "x1", `,"tool_name":"Bash"`),
					rig.codex("idle", "x1"),
				}
			},
			calls: []string{
				codexReportCall("w1:p1", "idle", "x1", ""),
				codexReportCall("w1:p1", "blocked", "x1", "waiting for approval: Bash"),
				codexReportCall("w1:p1", "idle", "x1", ""),
			},
			records: []string{"x1"},
		},
		{
			name: "a SessionStart from the shared app-server daemon registers nothing",
			setup: func(rig *chatRig) {
				rig.jail.procs = map[int]fakeProc{100: {77, "sh"}, 77: {1, "codex"}}
				rig.jail.argv = map[int][]string{77: {"codex", "app-server", "--listen", "unix://", "--managed-daemon"}}
				rig.jail.paneEnv = map[int]map[string]string{77: {"TMUX": rigSocket + ",1,0", "TMUX_PANE": "%0"}}
			},
			run: func(rig *chatRig) []herdrRun {
				return []herdrRun{rig.codex("start", "x1", `,"source":"startup"`), rig.codex("idle", "x1")}
			},
			logs: []string{"not an interactive Codex TUI"},
		},
		{
			name: "a SessionStart whose hook runs under no Codex process registers nothing",
			setup: func(rig *chatRig) {
				rig.jail.procs = map[int]fakeProc{100: {91, "sh"}, 91: {codexTUI, "tcsh"}, codexTUI: {80, "codex"}, 80: {1, "-zsh"}}
				rig.jail.argv[91] = []string{"tcsh", "-c", "pfm internal herdr-state --engine codex start"}
				rig.jail.paneEnv[91] = herdrPaneEnv(rig.herdr, "w9:p9")
			},
			run: func(rig *chatRig) []herdrRun {
				return []herdrRun{rig.codex("start", "x1", `,"source":"startup"`), rig.codex("idle", "x1")}
			},
			logs: []string{"is no Codex process"},
		},
		{
			name: "a session id that is no plain name registers nothing",
			run: func(rig *chatRig) []herdrRun {
				return []herdrRun{rig.codex("start", "x/../../escape", `,"source":"startup"`)}
			},
			logs: []string{"is not a plain name"},
		},
		{
			name: "an unmapped Stop is dropped",
			run:  func(rig *chatRig) []herdrRun { return []herdrRun{rig.codex("idle", "x1")} },
		},
		{
			name: "another thread's Stop while the pane's session is registered is dropped",
			run: func(rig *chatRig) []herdrRun {
				return []herdrRun{rig.codex("start", "x1", `,"source":"startup"`), rig.codex("idle", "t2")}
			},
			calls:   []string{codexReportCall("w1:p1", "idle", "x1", "")},
			records: []string{"x1"},
		},
		{
			name: "a new session in the same TUI takes the pane from the old one",
			run: func(rig *chatRig) []herdrRun {
				return []herdrRun{
					rig.codex("start", "x1", `,"source":"startup"`),
					rig.codex("start", "x2", `,"source":"startup"`),
					rig.codex("working", "x1"),
					rig.codex("working", "x2"),
				}
			},
			calls: []string{
				codexReportCall("w1:p1", "idle", "x1", ""),
				codexReportCall("w1:p1", "idle", "x2", ""),
				codexReportCall("w1:p1", "working", "x2", ""),
			},
			records: []string{"x2"},
		},
		{
			name: "SessionEnd releases the pane, clears its title and ends the registration",
			run: func(rig *chatRig) []herdrRun {
				return []herdrRun{
					rig.codex("start", "x1", `,"source":"startup"`),
					rig.codex("end", "x1", `,"reason":"other"`),
					rig.codex("idle", "x1"),
				}
			},
			calls: []string{
				codexReportCall("w1:p1", "idle", "x1", ""),
				`pane.release_agent {"pane_id":"w1:p1","source":"pfm","agent":"codex"}`,
				`pane.report_metadata {"pane_id":"w1:p1","source":"pfm","clear_title":true}`,
			},
		},
		{
			name: "a SessionEnd that swaps the session keeps the registration",
			run: func(rig *chatRig) []herdrRun {
				return []herdrRun{
					rig.codex("start", "x1", `,"source":"startup"`),
					rig.codex("end", "x1", `,"reason":"clear"`),
					rig.codex("working", "x1"),
				}
			},
			calls: []string{
				codexReportCall("w1:p1", "idle", "x1", ""),
				codexReportCall("w1:p1", "working", "x1", ""),
			},
			records: []string{"x1"},
		},
		{
			name: "a registration whose TUI is gone is removed",
			run: func(rig *chatRig) []herdrRun {
				started := rig.codex("start", "x1", `,"source":"startup"`)
				delete(rig.jail.procs, codexTUI)
				return []herdrRun{started, rig.codex("idle", "x1")}
			},
			calls: []string{codexReportCall("w1:p1", "idle", "x1", "")},
			logs:  []string{"the TUI process 90 of Codex session x1 is gone"},
		},
		{
			name: "a registration whose TUI now runs another command is removed",
			run: func(rig *chatRig) []herdrRun {
				started := rig.codex("start", "x1", `,"source":"startup"`)
				rig.jail.procs[codexTUI] = fakeProc{80, "vim"}
				return []herdrRun{started, rig.codex("idle", "x1")}
			},
			calls: []string{codexReportCall("w1:p1", "idle", "x1", "")},
			logs:  []string{"the TUI process 90 of Codex session x1 is gone or runs another command"},
		},
		{
			name: "a process table that cannot be read keeps every registration",
			run: func(rig *chatRig) []herdrRun {
				started := rig.codex("start", "x1", `,"source":"startup"`)
				rig.jail.procFails = map[int]error{codexTUI: errors.New("sysctl kern.proc: too many open files")}
				unread := rig.codex("idle", "x1")
				// A second TUI, in a plain terminal, sweeps the registry.
				rig.jail.procs[100] = fakeProc{95, "sh"}
				rig.jail.procs[95] = fakeProc{80, "codex"}
				rig.jail.argv[95] = []string{"codex"}
				rig.jail.paneEnv[95] = map[string]string{"TERM": "xterm"}
				swept := rig.codex("start", "y1", `,"source":"startup"`)
				rig.jail.procFails = nil
				return []herdrRun{started, unread, swept, rig.codex("working", "x1")}
			},
			calls: []string{
				codexReportCall("w1:p1", "idle", "x1", ""),
				codexReportCall("w1:p1", "working", "x1", ""),
			},
			logs:    []string{"could not read the TUI process 90 of Codex session x1"},
			records: []string{"x1", "y1"},
		},
		{
			name: "a registry sweep that fails still reports the new session",
			setup: func(rig *chatRig) {
				if err := os.MkdirAll(filepath.Join(rig.jail.lockDir, "codex-zz.pane", "kept"), 0o700); err != nil {
					rig.t.Fatal(err)
				}
			},
			run: func(rig *chatRig) []herdrRun {
				return []herdrRun{rig.codex("start", "x1", `,"source":"startup"`)}
			},
			calls:   []string{codexReportCall("w1:p1", "idle", "x1", "")},
			logs:    []string{"remove the Codex pane registration"},
			records: []string{"x1", "zz"},
		},
		{
			name: "a TUI in a plain terminal reports nowhere, silently",
			setup: func(rig *chatRig) {
				rig.jail.paneEnv = map[int]map[string]string{codexTUI: {"TERM": "xterm"}}
			},
			run: func(rig *chatRig) []herdrRun {
				return []herdrRun{rig.codex("start", "x1", `,"source":"startup"`), rig.codex("idle", "x1")}
			},
			records: []string{"x1"},
		},
		{
			name: "a codex exec run reports nowhere, silently",
			setup: func(rig *chatRig) {
				rig.jail.argv = map[int][]string{codexTUI: {"codex", "exec", "say hi"}}
			},
			run: func(rig *chatRig) []herdrRun {
				return []herdrRun{rig.codex("start", "x1", `,"source":"startup"`), rig.codex("idle", "x1")}
			},
			records: []string{"x1"},
		},
		{
			name: "a host without Herdr or the theme registers nothing and stays silent",
			setup: func(rig *chatRig) {
				delete(rig.env, "HERDR_BIN_PATH")
			},
			run: func(rig *chatRig) []herdrRun {
				return []herdrRun{rig.codex("start", "x1", `,"source":"startup"`), rig.codex("idle", "x1")}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rig := codexRig(t)
			if test.setup != nil {
				test.setup(rig)
			}
			for index, got := range test.run(rig) {
				if got.rc != 0 {
					t.Fatalf("run %d: rc=%d stderr=%q, want exit 0", index, got.rc, got.stderr)
				}
				if len(test.logs) == 0 && got.stderr != "" {
					t.Fatalf("run %d: stderr=%q, want a quiet run", index, got.stderr)
				}
			}
			rig.wantCalls(test.calls...)
			logged := rig.jail.logged(herdrNoSession)
			for _, want := range test.logs {
				if !strings.Contains(logged, want) {
					t.Fatalf("error log = %q, want a line naming %q", logged, want)
				}
			}
			if len(test.logs) == 0 && logged != "" {
				t.Fatalf("error log = %q, want none", logged)
			}
			if lines := strings.Count(logged, "\n"); len(test.logs) > 0 && lines > len(test.logs) {
				t.Fatalf("error log = %q, want one line per unmapped event", logged)
			}
			if got := codexRecords(t, rig.jail.lockDir); !slices.Equal(got, test.records) {
				t.Fatalf("registered sessions = %q, want %q", got, test.records)
			}
			if outside, _ := filepath.Glob(filepath.Join(filepath.Dir(rig.jail.lockDir), "*.pane")); len(outside) > 0 {
				t.Fatalf("pane records outside the registry: %q", outside)
			}
		})
	}
}

// On a host without Herdr a Codex TUI in a pfm chat still registers its tmux
// pane while the theme sync is on: the theme marks that chat server's state.
func TestHerdrCodexWithoutHerdrRegistersForTheTheme(t *testing.T) {
	rig := codexRig(t)
	delete(rig.env, "HERDR_BIN_PATH")
	rig.themeOn()
	rig.jail.paneEnv = map[int]map[string]string{codexTUI: {"TMUX": rigSocket + ",4000,0", "TMUX_PANE": "%3"}}
	rig.tmux.panePID = "80"
	rig.wantQuiet(rig.codex("start", "x1", `,"source":"startup"`))
	rig.wantQuiet(rig.codex("idle", "x1"))
	want := "socket=" + rigSocket + " set-option -g @pfm-state done"
	if calls := rig.themeStateCalls(); !slices.Contains(calls, want) {
		t.Fatalf("@pfm-state calls = %q, want %q", calls, want)
	}
	if got := codexRecords(t, rig.jail.lockDir); !slices.Equal(got, []string{"x1"}) {
		t.Fatalf("registered sessions = %q, want [x1]", got)
	}
}

// A Codex TUI inside a tmux server registers that server's pane from its own
// TMUX and TMUX_PANE: its events then take the tmux path, through the pane
// the TUI named, whatever $TMUX the hook carries.
func TestHerdrCodexInTmuxReportsThroughTheTUIsOwnPane(t *testing.T) {
	rig := codexRig(t)
	rig.jail.paneEnv = map[int]map[string]string{codexTUI: {"TMUX": rigSocket + ",4000,0", "TMUX_PANE": "%3"}}
	rig.env["TMUX"], rig.env["TMUX_PANE"] = "/tmp/tmux-1/cx-gone,94682,0", "%0"
	rig.tmux.panePID = "80"
	rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
	rig.wantQuiet(rig.codex("start", "x1", `,"source":"startup"`))
	rig.wantQuiet(rig.codex("idle", "x1"))
	asked := "socket=" + rigSocket + " display-message -p -t %3 #{session_name}\t#{pane_pid}"
	if calls := rig.tmux.allCalls(); !slices.Contains(calls, asked) {
		t.Fatalf("tmux calls =\n  %s\nwant %q", strings.Join(calls, "\n  "), asked)
	}
	if stored := rig.tmux.storedState(t); stored.State != "idle" || stored.SessionID != "x1" ||
		stored.Engine != "codex" {
		t.Fatalf("stored = %q, want codex session x1 idle", rig.tmux.stored)
	}
	if !slices.Contains(rig.herdr.calls(), codexReportCall("w1:p1", "idle", "x1", "")) {
		t.Fatalf("herdr requests = %q, want codex idle on w1:p1", rig.herdr.calls())
	}
}

// codexRecords are the session ids of the Codex pane registry, sorted.
func codexRecords(t *testing.T, lockDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(lockDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var sessions []string
	for _, entry := range entries {
		name := entry.Name()
		if session, ok := strings.CutPrefix(name, "codex-"); ok && strings.HasSuffix(session, ".pane") {
			sessions = append(sessions, strings.TrimSuffix(session, ".pane"))
		}
	}
	return sessions
}

// A Codex event of a session no TUI registered (the shared daemon's thread,
// another chat's) is the normal case on a host with Herdr: it exits 0 and
// writes no row. A record that exists but cannot be read still writes its row.
func TestCodexEventWithoutARegistrationIsSilent(t *testing.T) {
	t.Run("no codex-{id}.pane: exit 0, quiet, no row in no-session.log", func(t *testing.T) {
		rig := codexRig(t)
		rig.wantQuiet(rig.codex("idle", "x1"))
		rig.wantCalls()
		if logged := rig.jail.logged(herdrNoSession); logged != "" {
			t.Fatalf("no-session.log = %q, want no row for a session nothing registered", logged)
		}
	})
	t.Run("an unreadable record still writes its row", func(t *testing.T) {
		rig := codexRig(t)
		if err := os.MkdirAll(codexPanePath(rig.jail.lockDir, "x1"), 0o700); err != nil {
			t.Fatal(err)
		}
		got := rig.codex("idle", "x1")
		if got.rc != 0 || !strings.Contains(got.stderr, "read the pane of Codex session x1") {
			t.Fatalf("rc=%d stderr=%q, want exit 0 naming the unreadable record", got.rc, got.stderr)
		}
		if logged := rig.jail.logged(herdrNoSession); !strings.Contains(logged, "read the pane of Codex session x1") {
			t.Fatalf("no-session.log = %q, want the unreadable record's row", logged)
		}
	})
}
