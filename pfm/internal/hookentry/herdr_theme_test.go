package hookentry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// themeOn switches the `pfm theme` sync on in the rig's jail the way
// `pfm theme set` leaves it: a state file that says enabled.
func (rig *chatRig) themeOn() {
	rig.t.Helper()
	rig.jail.stateDir = filepath.Join(testjail.ShortRoot(rig.t), "theme")
	if err := os.MkdirAll(rig.jail.stateDir, 0o700); err != nil {
		rig.t.Fatal(err)
	}
	state := filepath.Join(rig.jail.stateDir, themesync.StateFileName)
	if err := os.WriteFile(state, []byte(`{"enabled":true}`), 0o600); err != nil {
		rig.t.Fatal(err)
	}
}

// themeStateCalls are the tmux calls that mark the server's @pfm-state.
func (rig *chatRig) themeStateCalls() []string {
	var calls []string
	for _, call := range rig.tmux.setOptionCalls() {
		if strings.Contains(call, " @pfm-state ") || strings.HasSuffix(call, " -gu @pfm-state") {
			calls = append(calls, call)
		}
	}
	return calls
}

func TestHerdrStateMarksTheServersAgentStateWhileTheThemeIsOn(t *testing.T) {
	for _, test := range []struct {
		name  string
		event string
		input string
		want  string
	}{
		{"a new session is idle", "start", `{"session_id":"s1","source":"startup"}`, "idle"},
		{"a resumed session is idle", "start", `{"session_id":"s1","source":"resume"}`, "idle"},
		{"a prompt submitted is working", "working", `{"session_id":"s1"}`, "working"},
		{"a permission request is blocked", "blocked", `{"session_id":"s1","message":"needs you"}`, "blocked"},
		{"a permission notification is blocked", "notification", `{"session_id":"s1","notification_type":"permission_prompt"}`, "blocked"},
		{"an elicitation notification is blocked", "notification", `{"session_id":"s1","notification_type":"elicitation_dialog"}`, "blocked"},
		{"a finished turn is done", "idle", `{"session_id":"s1"}`, "done"},
		{"the idle prompt fades done to idle", "notification", `{"session_id":"s1","notification_type":"idle_prompt"}`, "idle"},
		{"a compaction restart changes nothing", "start", `{"session_id":"s1","source":"compact"}`, ""},
		{"a session end clears the state", "end", `{"session_id":"s1","reason":"logout"}`, "-gu"},
		{"a /clear session swap changes nothing", "end", `{"session_id":"s1","reason":"clear"}`, ""},
		{"an unrelated notification changes nothing", "notification", `{"session_id":"s1","notification_type":"auth_success"}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			rig := newChatRig(t)
			rig.themeOn()
			rig.wantQuiet(rig.engine(test.event, test.input))
			calls := rig.themeStateCalls()
			if test.want == "" {
				if len(calls) != 0 {
					t.Fatalf("@pfm-state set for an event that changes no state: %v", calls)
				}
				return
			}
			want := "socket=" + rigSocket + " set-option -g @pfm-state " + test.want
			if test.want == "-gu" {
				want = "socket=" + rigSocket + " set-option -gu @pfm-state"
			}
			if len(calls) != 1 || calls[0] != want {
				t.Fatalf("@pfm-state calls = %v, want exactly [%s]", calls, want)
			}
		})
	}
}

func TestHerdrStateLeavesTheServersAgentStateAloneWhileTheThemeIsOff(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(rig *chatRig)
	}{
		{"no theme state directory wired", func(*chatRig) {}},
		{"a state directory with no state file", func(rig *chatRig) { rig.jail.stateDir = t.TempDir() }},
		{"a state file that says off", func(rig *chatRig) {
			rig.jail.stateDir = t.TempDir()
			path := filepath.Join(rig.jail.stateDir, themesync.StateFileName)
			if err := os.WriteFile(path, []byte(`{"enabled":false}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rig := newChatRig(t)
			test.setup(rig)
			rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
			if calls := rig.themeStateCalls(); len(calls) != 0 {
				t.Fatalf("@pfm-state set with the theme off: %v", calls)
			}
		})
	}
}

// A host with tmux and the theme but no Herdr still gets its colours: the
// state door is not behind the Herdr gate.
func TestHerdrStateMarksTheServerOnAHostWithoutHerdr(t *testing.T) {
	rig := newChatRig(t)
	rig.themeOn()
	delete(rig.env, "HERDR_BIN_PATH")
	rig.jail.onPath = false
	rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
	want := "socket=" + rigSocket + " set-option -g @pfm-state working"
	if calls := rig.themeStateCalls(); len(calls) != 1 || calls[0] != want {
		t.Fatalf("@pfm-state calls = %v, want [%s]", calls, want)
	}
	for _, call := range rig.tmux.setOptionCalls() {
		if strings.Contains(call, herdrStoredOption) {
			t.Fatalf("Herdr state stored on a host without Herdr: %s", call)
		}
	}
}

// A `claude -p` the chat's own Bash tool started must not recolour the chat.
func TestHerdrStateNestedEngineDoesNotMarkTheServer(t *testing.T) {
	rig := newChatRig(t)
	rig.themeOn()
	rig.jail.procs = map[int]fakeProc{100: {70, "pfm"}, 70: {60, "claude"}, 60: {50, "bash"}, 50: {1, "claude"}}
	rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
	if calls := rig.themeStateCalls(); len(calls) != 0 {
		t.Fatalf("a nested engine marked the server: %v", calls)
	}
}

// The mark is cosmetic: a tmux that refuses it is logged (fail-open) and the
// Herdr report goes on.
func TestHerdrStateThemeMarkFailureIsFailOpen(t *testing.T) {
	rig := newChatRig(t)
	rig.themeOn()
	delete(rig.env, "HERDR_BIN_PATH")
	rig.jail.onPath = false
	rig.tmux.failOn = "set-option"
	got := rig.engine("working", `{"session_id":"s1"}`)
	if got.rc != 0 || !strings.Contains(got.stderr, "tmux theme state working") ||
		!strings.Contains(got.stderr, "(fail-open)") {
		t.Fatalf("rc=%d stderr=%q, want exit 0 and a named fail-open line", got.rc, got.stderr)
	}
}

// With Herdr present the mark failing leaves the Herdr store untouched by it:
// the report still reaches Herdr afterwards.
func TestHerdrStateThemeMarkDoesNotStopTheHerdrReport(t *testing.T) {
	rig := newChatRig(t)
	rig.themeOn()
	rig.seed(rig.registered("idle", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
	got := rig.engine("working", `{"session_id":"s1"}`)
	rig.wantQuiet(got)
	if !strings.Contains(rig.tmux.stored, `"state":"working"`) {
		t.Fatalf("Herdr store = %s, want the working state stored beside the theme mark", rig.tmux.stored)
	}
	if len(rig.themeStateCalls()) != 1 {
		t.Fatalf("theme marks = %v", rig.themeStateCalls())
	}
}

// An engine running in the user's own tmux (not a pfm chat server) leaves that
// server alone: only pfm chat servers carry the theme, and `pfm theme off`
// clears only those.
func TestHerdrStateLeavesANonChatTmuxServerUnmarked(t *testing.T) {
	rig := newChatRig(t)
	rig.themeOn()
	delete(rig.env, "HERDR_BIN_PATH")
	rig.jail.onPath = false
	rig.env["TMUX"] = "/private/tmp/tmux-501/default,1,0"
	rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
	if calls := rig.themeStateCalls(); len(calls) != 0 {
		t.Fatalf("the user's own tmux server was marked: %v", calls)
	}
}
