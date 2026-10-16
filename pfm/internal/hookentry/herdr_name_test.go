package hookentry

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// namedRig is a chat that reported idle to w1:p1 (client 4242) and holds a name.
func namedRig(t *testing.T, chatName string) *chatRig {
	t.Helper()
	rig := newChatRig(t)
	rig.seed(rig.registered("idle", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
	rig.tmux.chatName = chatName
	return rig
}

// carriedRig is namedRig whose panes already carry the chat's name "Fix It".
func carriedRig(t *testing.T) *chatRig {
	t.Helper()
	rig := newChatRig(t)
	stored := rig.registered("idle", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")})
	stored.Name = "Fix It"
	rig.seed(stored)
	rig.tmux.chatName = "Fix It"
	return rig
}

// Every name writer ends in a window rename, which fires the one `name` verb:
// the five triggers differ only in the name they stored.
func TestHerdrStateNameSyncRenamesAndTitlesEveryLivePane(t *testing.T) {
	for _, test := range []struct{ trigger, chatName, agent string }{
		{"N1 Claude /rename", "Fix the auth bug", "fix-the-auth-bug"},
		{"N2 Codex rename", "codex_Audit Run", "codex_audit-run"},
		{"N3 chat name", "HerDer_Ultimate_Integration", "herder_ultimate_integration"},
		{"N4 chat new --name", "9 Lives", "c9-lives"},
		{"N5 chat branch", "auth-fix (branch)", "auth-fix-branch"},
	} {
		t.Run(test.trigger, func(t *testing.T) {
			rig := namedRig(t, test.chatName)
			rig.wantQuiet(rig.hook("name"))
			rig.wantCalls(listCall, `agent.list {}`, renameCall("w1:p1", test.agent), titleCall("w1:p1", test.chatName))
			if state := rig.tmux.storedState(t); state.Name != test.chatName || state.State != "idle" {
				t.Fatalf("stored = %q, want the name %q kept beside the state", rig.tmux.stored, test.chatName)
			}
		})
	}
}

// A window rename that leaves the chat's name as the panes were last named
// after (a statusline repaint, a re-sync) sends Herdr nothing; a pane that moved
// is still named, and a pane whose naming failed is named again next time.
func TestHerdrStateNameSyncSkipsANameThePanesAlreadyCarry(t *testing.T) {
	t.Run("an unchanged name names no pane", func(t *testing.T) {
		rig := carriedRig(t)
		rig.wantQuiet(rig.hook("name"))
		rig.wantCalls(listCall)
	})
	t.Run("a pane that moved is named under the unchanged name", func(t *testing.T) {
		rig := carriedRig(t)
		rig.herdr.move("w1:p1", "w2:p1")
		rig.wantQuiet(rig.hook("name"))
		rig.wantCalls(listCall, reportCall("w2:p1", "idle", ""), `agent.list {}`,
			renameCall("w2:p1", "fix-it"), titleCall("w2:p1", "Fix It"))
	})
	t.Run("a refused naming is retried by the next rename", func(t *testing.T) {
		rig := namedRig(t, "Fix It")
		rig.herdr.refuse["pane.report_metadata"] = "invalid_params"
		if got := rig.hook("name"); !strings.Contains(got.stderr, "title pane") {
			t.Fatalf("stderr = %q, want the refused title named", got.stderr)
		}
		delete(rig.herdr.refuse, "pane.report_metadata")
		before := len(rig.herdr.calls())
		rig.wantQuiet(rig.hook("name"))
		want := []string{listCall, `agent.list {}`, renameCall("w1:p1", "fix-it"), titleCall("w1:p1", "Fix It")}
		if got := rig.herdr.calls()[before:]; !slices.Equal(got, want) {
			t.Fatalf("second rename requests = %q, want %q", got, want)
		}
	})
}

func TestHerdrStateNameSyncPicksTheFirstFreeAgentName(t *testing.T) {
	for _, test := range []struct {
		name   string
		agents []fakeAgent
		want   string
	}{
		{"another pane holds the name", []fakeAgent{{"fix-it", "w9:p9"}}, "fix-it-2"},
		{"another pane holds it and -2", []fakeAgent{{"fix-it", "w9:p9"}, {"fix-it-2", "w8:p8"}}, "fix-it-3"},
		{"the chat's own pane holds it", []fakeAgent{{"fix-it", "w1:p1"}}, "fix-it"},
		{"unnamed agents hold nothing", []fakeAgent{{"", "w9:p9"}}, "fix-it"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rig := namedRig(t, "Fix It")
			rig.herdr.setAgents(test.agents...)
			rig.wantQuiet(rig.hook("name"))
			rig.wantCalls(listCall, `agent.list {}`, renameCall("w1:p1", test.want), titleCall("w1:p1", "Fix It"))
		})
	}
}

func TestHerdrStateNameSyncTriesEverySuffixThenNamesTheRefusal(t *testing.T) {
	rig := namedRig(t, "HerDer_Ultimate_Integration")
	taken := map[string]bool{}
	want := []string{listCall, `agent.list {}`}
	for range 9 {
		candidate := herdr.AgentName("HerDer_Ultimate_Integration", taken)
		taken[candidate] = true
		rig.herdr.refuseRename[candidate] = "name_taken"
		want = append(want, renameCall("w1:p1", candidate))
	}
	got := rig.hook("name")
	rig.wantCalls(want...)
	const refused = `name pane w1:p1 "herder_ultimate_integration": refused name_taken`
	if got.rc != 0 || !strings.Contains(got.stderr, refused) {
		t.Fatalf("rc=%d stderr=%q, want exit 0 and %q", got.rc, got.stderr, refused)
	}
	if logged := rig.jail.logged("cc-1"); !strings.Contains(logged, refused) {
		t.Fatalf("cc-1.log = %q, want the refusal kept where the discarded stderr cannot lose it", logged)
	}
}

func TestHerdrStateNameSyncErrorsAreNamedNeverSilent(t *testing.T) {
	t.Run("agent.list failing: no rename, stderr names it", func(t *testing.T) {
		rig := namedRig(t, "Fix It")
		rig.herdr.refuse["agent.list"] = "internal_error"
		got := rig.hook("name")
		rig.wantCalls(listCall, `agent.list {}`)
		if got.rc != 0 || !strings.Contains(got.stderr, "list agents") ||
			!strings.Contains(got.stderr, "internal_error") {
			t.Fatalf("rc=%d stderr=%q, want exit 0 naming agent.list and the refusal", got.rc, got.stderr)
		}
	})
	t.Run("a refused title is a stderr line after the rename", func(t *testing.T) {
		rig := namedRig(t, "Fix It")
		rig.herdr.refuse["pane.report_metadata"] = "invalid_params"
		got := rig.hook("name")
		rig.wantCalls(listCall, `agent.list {}`, renameCall("w1:p1", "fix-it"), titleCall("w1:p1", "Fix It"))
		if got.rc != 0 || !strings.Contains(got.stderr, `title pane w1:p1 "Fix It"`) ||
			!strings.Contains(got.stderr, "invalid_params") {
			t.Fatalf("rc=%d stderr=%q, want exit 0 naming the title and the refusal", got.rc, got.stderr)
		}
	})
	t.Run("an unnamed chat gets no rename and no title", func(t *testing.T) {
		rig := namedRig(t, "")
		rig.wantQuiet(rig.hook("name"))
		rig.wantCalls(listCall)
		if state := rig.tmux.storedState(t); state.Name != "" {
			t.Fatalf("stored name = %q, want none for an unnamed chat", state.Name)
		}
	})
	t.Run("a pane nothing was reported to is not named, the name is still kept", func(t *testing.T) {
		rig := newChatRig(t)
		rig.seed(rig.registered("", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
		rig.tmux.chatName = "Fix It"
		rig.wantQuiet(rig.hook("name"))
		rig.wantCalls(listCall)
		if state := rig.tmux.storedState(t); state.Name != "Fix It" {
			t.Fatalf("stored name = %q, want it kept for the first report", state.Name)
		}
	})
	t.Run("a title over Herdr's cap is cut on a rune boundary", func(t *testing.T) {
		rig := namedRig(t, strings.Repeat("é", 100))
		rig.wantQuiet(rig.hook("name"))
		calls := rig.herdr.calls()
		if len(calls) != 4 {
			t.Fatalf("requests = %q, want list, agent.list, rename, title", calls)
		}
		_, params, _ := strings.Cut(calls[3], " ")
		var title struct {
			Title string `json:"title"`
		}
		if err := json.Unmarshal(
			[]byte(params),
			&title,
		); err != nil ||
			len([]rune(title.Title)) != herdr.MaxTitleRunes {
			t.Fatalf("title params %s (%v), want %d runes", params, err, herdr.MaxTitleRunes)
		}
	})
}

// A session start names every pane the chat is attached from; a compaction does
// not start a session, and a plain event renames nothing.
func TestHerdrStateSessionStartNamesThePanes(t *testing.T) {
	for _, source := range []string{"startup", "resume", "clear"} {
		t.Run(source, func(t *testing.T) {
			rig := namedRig(t, "Fix It")
			rig.wantQuiet(rig.engine("start", `{"session_id":"s1","source":"`+source+`"}`))
			rig.wantCalls(
				listCall, reportCall("w1:p1", "idle", ""),
				`agent.list {}`, renameCall("w1:p1", "fix-it"), titleCall("w1:p1", "Fix It"),
			)
		})
	}
	t.Run("compaction restarts mid-turn: nothing", func(t *testing.T) {
		rig := namedRig(t, "Fix It")
		rig.wantQuiet(rig.engine("start", `{"session_id":"s1","source":"compact"}`))
		rig.wantCalls()
	})
	t.Run("a working event renames nothing", func(t *testing.T) {
		rig := carriedRig(t)
		rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
		rig.wantCalls(listCall, reportCall("w1:p1", "working", ""))
	})
	t.Run("a working event names the panes for a name they lack", func(t *testing.T) {
		rig := namedRig(t, "Fix It")
		rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
		rig.wantCalls(
			listCall, reportCall("w1:p1", "working", ""),
			`agent.list {}`, renameCall("w1:p1", "fix-it"), titleCall("w1:p1", "Fix It"),
		)
	})
	t.Run("a client that attaches in another pane names that pane only", func(t *testing.T) {
		rig := carriedRig(t)
		rig.herdr.addPane("w2:p3", "term_3", 5151)
		rig.tmux.clients = []string{"4242", "5151"}
		rig.clientEnv(5151, "w2:p3")
		rig.outsideHerdr(4242)
		rig.wantQuiet(rig.hook("attach", "--client", "5151"))
		rig.wantCalls(
			listCall, `pane.process_info {"pane_id":"w2:p3"}`, reportCall("w2:p3", "idle", ""),
			`agent.list {}`, renameCall("w2:p3", "fix-it"), titleCall("w2:p3", "Fix It"),
		)
	})
}

// tmux discards the stderr of a hook it runs: every reporter error is also in
// the session's log, and a log that cannot be written reaches stderr.
func TestHerdrStateErrorsLandInTheSessionLog(t *testing.T) {
	t.Run("a refused name leaves a line in the log", func(t *testing.T) {
		rig := namedRig(t, "Fix It")
		rig.herdr.refuse["agent.rename"] = "pane_busy"
		rig.hook("name")
		if logged := rig.jail.logged(
			"cc-1",
		); !strings.Contains(logged, "pane_busy") ||
			!strings.Contains(logged, "name pane w1:p1") {
			t.Fatalf("cc-1.log = %q, want the refused name", logged)
		}
	})
	t.Run("a log that cannot be written is told on stderr", func(t *testing.T) {
		rig := namedRig(t, "Fix It")
		rig.herdr.refuse["agent.rename"] = "pane_busy"
		rig.jail.logFails = errors.New("disk full")
		got := rig.hook("name")
		if got.rc != 0 || !strings.Contains(got.stderr, "pane_busy") ||
			!strings.Contains(got.stderr, "log error: disk full") {
			t.Fatalf("rc=%d stderr=%q, want the refusal and the failed log append", got.rc, got.stderr)
		}
	})
	t.Run("a usage error is logged too, under no session", func(t *testing.T) {
		rig := newChatRig(t)
		got := rig.run([]string{"bogus"}, "")
		if got.rc != 1 || !strings.Contains(rig.jail.logged("no-session"), "unknown event") {
			t.Fatalf(
				"rc=%d no-session.log = %q, want exit 1 and the usage error logged",
				got.rc,
				rig.jail.logged("no-session"),
			)
		}
	})
}

// OpenCode's plugin forwards a title change of its main session: the reporter
// issues the pair every other name writer issues, and tmux's window-renamed hook
// then runs the name sync.
func TestHerdrStateTitleEventStoresTheChatName(t *testing.T) {
	run := func(rig *chatRig, stdin string) herdrRun {
		rig.t.Helper()
		rig.env["TMUX_PANE"] = "%3"
		return rig.run([]string{"--engine", "opencode", "title"}, stdin)
	}
	t.Run("the exact tmux command: window name and @pfm_chat_name in one call", func(t *testing.T) {
		rig := newChatRig(t)
		rig.wantQuiet(run(rig, `{"session_id":"s1","title":"Fix: auth.go bug"}`))
		want := "socket=" + rigSocket + " set-option -t cc-1 @pfm_chat_name Fix: auth.go bug ; rename-window -t %3 " +
			spawn.WindowName("Fix: auth.go bug")
		calls := rig.tmux.allCalls()
		if len(calls) != 2 || calls[1] != want {
			t.Fatalf("tmux calls = %q, want the session lookup, then %q", calls, want)
		}
		if files := rig.jail.lockFiles(); len(files) != 0 {
			t.Fatalf("lock files = %q, want none: the title event stores nothing in @pfm_herdr", files)
		}
		rig.wantCalls()
	})
	t.Run("a title ending in ; keeps its ; escaped from tmux's command separator", func(t *testing.T) {
		rig := newChatRig(t)
		rig.wantQuiet(run(rig, `{"session_id":"s1","title":"Fix it;"}`))
		want := "socket=" + rigSocket + ` set-option -t cc-1 @pfm_chat_name Fix it\; ; rename-window -t %3 ` +
			spawn.WindowName("Fix it;")
		if calls := rig.tmux.allCalls(); len(calls) != 2 || calls[1] != want {
			t.Fatalf("tmux calls = %q, want the session lookup, then %q", calls, want)
		}
	})
	t.Run("the name sync that follows names the pane after the title", func(t *testing.T) {
		rig := namedRig(t, "")
		rig.wantQuiet(run(rig, `{"session_id":"s1","title":"Fix It"}`))
		rig.wantQuiet(rig.hook("name"))
		rig.wantCalls(listCall, `agent.list {}`, renameCall("w1:p1", "fix-it"), titleCall("w1:p1", "Fix It"))
	})
	t.Run("an empty title is a named error and renames nothing", func(t *testing.T) {
		rig := newChatRig(t)
		got := run(rig, `{"session_id":"s1","title":"  "}`)
		if got.rc != 0 || !strings.Contains(got.stderr, "empty title") || rig.tmux.window != "" {
			t.Fatalf(
				"rc=%d stderr=%q window=%q, want exit 0, the empty title named, no rename",
				got.rc,
				got.stderr,
				rig.tmux.window,
			)
		}
	})
	t.Run("a failing tmux rename is named and logged", func(t *testing.T) {
		rig := newChatRig(t)
		rig.tmux.failOn = "rename-window"
		got := run(rig, `{"session_id":"s1","title":"Fix It"}`)
		if got.rc != 0 || !strings.Contains(got.stderr, "rename-window") ||
			!strings.Contains(rig.jail.logged("cc-1"), "rename-window") {
			t.Fatalf(
				"rc=%d stderr=%q log=%q, want the failed rename named in both",
				got.rc,
				got.stderr,
				rig.jail.logged("cc-1"),
			)
		}
	})
	t.Run("a title from a nested engine stays silent", func(t *testing.T) {
		rig := newChatRig(t)
		rig.jail.procs = map[int]fakeProc{100: {70, "pfm"}, 70: {60, "node"}, 60: {50, "bash"}, 50: {1, "claude"}}
		rig.wantQuiet(run(rig, `{"session_id":"s1","title":"Fix It"}`))
		if rig.tmux.window != "" || rig.tmux.chatName != "" {
			t.Fatalf(
				"window %q, chat name %q, want a nested engine to rename nothing",
				rig.tmux.window,
				rig.tmux.chatName,
			)
		}
	})
}
