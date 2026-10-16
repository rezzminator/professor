package hookentry

import (
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// staleTmux is what Codex's shell snapshot exports into every hook it runs: the
// $TMUX and $TMUX_PANE of a chat server long gone.
const staleTmux = "/private/tmp/tmux-501/cx-1-2-3,94682,0"

// A fresh $TMUX, whose server pid is the tmux the hook runs under, is believed
// as it stands: no pane process environment is read.
func TestHerdrFreshTmuxReportsThroughItsOwnServer(t *testing.T) {
	rig := newChatRig(t)
	rig.env["TMUX"], rig.env["TMUX_PANE"] = rigSocket+",40,0", "%0"
	rig.jail.procs = map[int]fakeProc{100: {90, "pfm"}, 90: {50, "zsh"}, 50: {40, "claude"}, 40: {1, "tmux"}}
	rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
	rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
	calls := rig.tmux.allCalls()
	if len(calls) == 0 || !strings.HasPrefix(calls[0], "socket="+rigSocket+" display-message -p -t %0 ") {
		t.Fatalf("tmux calls = %q, want the first on %s asking about pane %%0", calls, rigSocket)
	}
	rig.wantCalls(listCall, reportCall("w1:p1", "idle", ""))
}

// An engine-fired event believes $TMUX only while the server pid it names is
// an ancestor of the hook; a tmux process met first makes the pane process
// below it, whose own environment tmux set, the source of the socket and pane.
func TestHerdrStaleTmuxReportsThroughTheServerTheEngineRunsUnder(t *testing.T) {
	// The server's command: its binary on macOS, the title tmux sets on Linux.
	for _, server := range []string{"tmux", "/opt/homebrew/bin/tmux", "tmux: server (/tmp/tmux-1000/default)"} {
		t.Run(server, func(t *testing.T) {
			rig := newChatRig(t)
			rig.env["TMUX"], rig.env["TMUX_PANE"] = staleTmux, "%0"
			// The hook (pfm) under the snapshot shell under the engine, the pane
			// process tmux 40 started.
			rig.jail.procs = map[int]fakeProc{100: {90, "pfm"}, 90: {50, "zsh"}, 50: {40, "claude"}, 40: {1, server}}
			rig.jail.paneEnv = map[int]map[string]string{50: {"TMUX": rigSocket + ",40,0", "TMUX_PANE": "%3"}}
			rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
			rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
			calls := rig.tmux.allCalls()
			if len(calls) == 0 || !strings.HasPrefix(calls[0], "socket="+rigSocket+" display-message -p -t %3 ") {
				t.Fatalf("tmux calls = %q, want the first on %s asking about pane %%3", calls, rigSocket)
			}
			for _, call := range calls {
				if !strings.HasPrefix(call, "socket="+rigSocket+" ") {
					t.Fatalf("tmux call %q, want every call on the engine's own server %s", call, rigSocket)
				}
			}
			rig.wantCalls(listCall, reportCall("w1:p1", "idle", ""))
		})
	}
}

// No tmux above the hook: the engine runs outside tmux, so a stale $TMUX is
// ignored and its process environment's Herdr pane is the target, even when
// the dead server's pid now belongs to an ancestor that is no tmux, or $TMUX
// names no server pid and the engine's parent is pid 0 (a process entered
// into a pid namespace, as `docker exec` starts it).
func TestHerdrStaleTmuxOutsideTmuxReportsToTheProcessPane(t *testing.T) {
	for _, test := range []struct {
		name, tmux string
		root       int
	}{
		{"a dead server", staleTmux, 1},
		{"its pid reused by the engine", "/private/tmp/tmux-501/cx-1-2-3,50,0", 1},
		{"no server pid, the engine's parent pid 0", "/private/tmp/tmux-501/cx-1-2-3", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			rig := newChatRig(t)
			rig.env["TMUX"], rig.env["TMUX_PANE"] = test.tmux, "%0"
			rig.jail.procs = map[int]fakeProc{100: {90, "pfm"}, 90: {50, "zsh"}, 50: {test.root, "claude"}}
			rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
			if calls := rig.tmux.allCalls(); len(calls) != 0 {
				t.Fatalf("tmux calls = %q, want none outside tmux", calls)
			}
			requests := rig.herdr.requests()
			if len(requests) != 1 || !strings.Contains(requests[0], `"pane_id":"w9:p9"`) ||
				!strings.Contains(requests[0], `"state":"working"`) {
				t.Fatalf("herdr requests = %q, want one working report to the process pane w9:p9", requests)
			}
		})
	}
}

// A stale $TMUX whose pane process environment cannot be read reports nothing
// and says so in the no-session error log.
func TestHerdrStaleTmuxWithAnUnreadablePaneEnvironmentLogs(t *testing.T) {
	for _, test := range []struct {
		name    string
		paneEnv map[int]map[string]string
		logs    string
	}{
		{"unreadable", nil, "environment of tmux pane process 50"},
		{"no TMUX in it", map[int]map[string]string{50: {"TERM": "xterm"}}, "no TMUX and TMUX_PANE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rig := newChatRig(t)
			rig.env["TMUX"], rig.env["TMUX_PANE"] = staleTmux, "%0"
			rig.jail.procs = map[int]fakeProc{100: {50, "pfm"}, 50: {40, "claude"}, 40: {1, "tmux"}}
			rig.jail.paneEnv = test.paneEnv
			got := rig.engine("working", `{"session_id":"s1"}`)
			if got.rc != 0 || !strings.Contains(got.stderr, test.logs) ||
				!strings.Contains(rig.jail.logged(herdrNoSession), test.logs) {
				t.Fatalf("rc=%d stderr=%q log=%q, want exit 0 and %q on stderr and in the log",
					got.rc, got.stderr, rig.jail.logged(herdrNoSession), test.logs)
			}
			if calls := rig.tmux.allCalls(); len(calls) != 0 {
				t.Fatalf("tmux calls = %q, want none on a server the hook does not run under", calls)
			}
			rig.wantCalls()
		})
	}
}

// Codex's memory consolidation thread fires hooks from the chat's own Codex
// process with "transcript_path": null; the run drops it before any tmux or
// Herdr call. Which payloads count as that thread is TestThreadWithoutRollout's.
func TestHerdrCodexThreadWithoutRolloutReportsNothing(t *testing.T) {
	rig := newChatRig(t)
	rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
	before := rig.tmux.stored
	payload := `{"session_id":"m1","transcript_path":null,"cwd":"/h/.codex/memories"}`
	rig.wantQuiet(rig.run([]string{"--engine", "codex", "working"}, payload))
	if calls := rig.tmux.allCalls(); len(calls) != 0 {
		t.Fatalf("tmux calls = %q, want none for a thread with no rollout", calls)
	}
	rig.wantCalls()
	if rig.tmux.stored != before {
		t.Fatalf("stored = %q, want it untouched (was %q)", rig.tmux.stored, before)
	}
}

// The Codex thread probe reads JSON null only: a string, an absent key or a
// payload that is not an object all keep reporting.
func TestThreadWithoutRollout(t *testing.T) {
	for _, test := range []struct {
		engine string
		raw    string
		want   bool
	}{
		{"cx", `{"transcript_path":null}`, true},
		{"cx", `{"transcript_path": null }`, true},
		{"cx", `{"transcript_path":"/r.jsonl"}`, false},
		{"cx", `{"transcript_path":""}`, false},
		{"cx", `{}`, false},
		{"cl", `{"transcript_path":null}`, false},
	} {
		if got := threadWithoutRollout(pfmengine.ID(test.engine), []byte(test.raw)); got != test.want {
			t.Errorf("threadWithoutRollout(%s, %s) = %v, want %v", test.engine, test.raw, got, test.want)
		}
	}
}
