package hookentry

import (
	"strings"
	"testing"
)

func (rig *chatRig) wantRegistry(want map[string]herdrClient) {
	rig.t.Helper()
	got := rig.tmux.storedState(rig.t).Clients
	if len(got) != len(want) {
		rig.t.Fatalf("registered clients = %v, want %v", got, want)
	}
	for pid, client := range want {
		if got[pid] != client {
			rig.t.Fatalf("registered clients = %v, want %v", got, want)
		}
	}
}

func (rig *chatRig) namesNothingOf(pane string, from int) {
	rig.t.Helper()
	for _, call := range rig.herdr.calls()[from:] {
		if strings.Contains(call, `"`+pane+`"`) {
			rig.t.Fatalf("request %q names the vacated pane %s", call, pane)
		}
	}
}

// A tmux client that attached from a Herdr pane is registered with that pane
// and the chat's stored state is reported to it.
func TestHerdrStateAttachRegistersTheClientThatAttached(t *testing.T) {
	t.Run("attached from w1:p1: registered, the stored highest-seq state re-reported", func(t *testing.T) {
		rig := newChatRig(t)
		state := rig.registered("blocked", nil)
		state.Message, state.Seq, state.Engine = "approve?", 12000, "claude"
		rig.seed(state)
		rig.tmux.clients = []string{"4242"}
		rig.sessionEnv("w1:p1")
		rig.wantQuiet(rig.hook("attach", "--client", "4242"))
		rig.wantCalls(listCall, `pane.process_info {"pane_id":"w1:p1"}`, reportCall("w1:p1", "blocked", "approve?"))
		rig.wantRegistry(map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")})
		if stored := rig.tmux.storedState(t); stored.Seq != 12000 || stored.State != "blocked" {
			t.Fatalf("stored = %q, want the state and its seq kept", rig.tmux.stored)
		}
		for _, call := range rig.tmux.allCalls() {
			if !strings.HasPrefix(call, "socket="+rigSocket+" ") {
				t.Fatalf("tmux call %q, want every call on the explicit socket", call)
			}
		}
	})
	for name, env := range map[string]string{
		"pane variable removed":           "HERDR_ENV=1\n-HERDR_PANE_ID\nHERDR_SOCKET_PATH=SOCKET\n",
		"a terminal without Herdr":        "-HERDR_ENV\n-HERDR_PANE_ID\n-HERDR_SOCKET_PATH\n-HERDR_BIN_PATH\n",
		"socket variable removed":         "HERDR_ENV=1\nHERDR_PANE_ID=w1:p1\n-HERDR_SOCKET_PATH\n",
		"marker not set to the Herdr one": "HERDR_ENV=0\nHERDR_PANE_ID=w1:p1\nHERDR_SOCKET_PATH=SOCKET\n",
	} {
		t.Run(name+": nothing registered, nothing sent", func(t *testing.T) {
			rig := newChatRig(t)
			rig.seed(rig.registered("working", nil))
			before := rig.tmux.stored
			rig.tmux.clients = []string{"4242"}
			rig.tmux.env = strings.ReplaceAll(env, "=SOCKET\n", "="+rig.herdr.path+"\n")
			rig.wantQuiet(rig.hook("attach", "--client", "4242"))
			rig.wantCalls()
			if rig.tmux.stored != before || len(rig.tmux.setOptionCalls()) != 0 {
				t.Fatalf("stored = %q (was %q), want it untouched", rig.tmux.stored, before)
			}
		})
	}
	// An engine that reports its session only at the first turn (Codex) is
	// invisible in Herdr until prompted: an attach with nothing stored yet
	// reports idle under the engine whose socket prefix the session carries, and
	// stores it with the seq untouched so any engine event still wins.
	for _, test := range []struct{ session, agent string }{
		{"cc-1", "claude"}, {"cx-1", "codex"}, {"ox-1", "opencode"},
	} {
		t.Run("nothing reported yet in "+test.session+": idle is reported under "+test.agent, func(t *testing.T) {
			rig := newChatRig(t)
			rig.tmux.clients = []string{"4242"}
			rig.sessionEnv("w1:p1")
			rig.wantQuiet(rig.run(
				[]string{"attach", "--socket", rigSocket, "--session", test.session, "--client", "4242"}, "",
			))
			rig.wantCalls(
				listCall, `pane.process_info {"pane_id":"w1:p1"}`,
				`pane.report_agent {"pane_id":"w1:p1","source":"pfm","agent":"`+test.agent+`","state":"idle"}`,
			)
			rig.wantRegistry(map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")})
			stored := rig.tmux.storedState(t)
			if stored.Engine != test.agent || stored.State != "idle" || stored.Seq != 0 {
				t.Fatalf("stored = %q, want engine %s, state idle and seq 0", rig.tmux.stored, test.agent)
			}
		})
	}
	t.Run("nothing reported yet in a session no engine owns: registered, no state", func(t *testing.T) {
		rig := newChatRig(t)
		rig.tmux.clients = []string{"4242"}
		rig.sessionEnv("w1:p1")
		args := []string{"attach", "--socket", rigSocket, "--session", "zz-1", "--client", "4242"}
		rig.wantQuiet(rig.run(args, ""))
		rig.wantCalls(listCall, `pane.process_info {"pane_id":"w1:p1"}`)
		rig.wantRegistry(map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")})
		if stored := rig.tmux.storedState(t); stored.State != "" || stored.Engine != "" {
			t.Fatalf("stored = %q, want no state", rig.tmux.stored)
		}
	})
	t.Run("an engine event after the fallback idle wins over it", func(t *testing.T) {
		rig := newChatRig(t)
		rig.tmux.clients = []string{"4242"}
		rig.sessionEnv("w1:p1")
		rig.wantQuiet(rig.hook("attach", "--client", "4242"))
		rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
		if stored := rig.tmux.storedState(t); stored.State != "working" || stored.SessionID != "s1" {
			t.Fatalf("stored = %q, want the engine's working event kept", rig.tmux.stored)
		}
		if got := rig.herdr.calls(); got[len(got)-1] != reportCall("w1:p1", "working", "") {
			t.Fatalf("requests = %q, want the working report last", got)
		}
	})
	// tmux keeps the Herdr variables of the LAST client that attached in the
	// session environment: a client that attached earlier without Herdr and
	// then resizes must not be registered with that other client's pane, which
	// would keep the pane reported to after its own client detached.
	t.Run("a resize of a never-registered client registers nothing and sends nothing", func(t *testing.T) {
		rig := newChatRig(t)
		rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
		before := rig.tmux.stored
		rig.tmux.clients = []string{"4242", "777"}
		rig.sessionEnv("w1:p1")
		rig.wantQuiet(rig.hook("sync", "--client", "777"))
		rig.wantRegistry(map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")})
		for _, call := range rig.herdr.calls() {
			if strings.HasPrefix(call, "pane.report") {
				t.Fatalf("request %q, want nothing reported for a client that never attached from Herdr", call)
			}
		}
		if rig.tmux.stored != before {
			t.Fatalf("stored = %q (was %q), want it untouched", rig.tmux.stored, before)
		}
	})
	// The pane id tmux copied is the one the pane's shell started with: a pane
	// moved before the attach carries a vacated id, or one Herdr gave to another
	// terminal. The client is bound to the pane its pid runs in, never to that id.
	for name, reuse := range map[string]bool{"vacated": false, "reused by another terminal": true} {
		t.Run("an attach naming a "+name+" pane id binds the pane running the client", func(t *testing.T) {
			rig := newChatRig(t)
			rig.herdr.addPane("w2:p1", "term_2", 4343)
			rig.herdr.move("w2:p1", "w6:p3")
			if reuse {
				rig.herdr.reassign("w2:p1", "term_9")
			}
			rig.seed(rig.registered("idle", nil))
			rig.tmux.clients = []string{"4343"}
			rig.sessionEnv("w2:p1")
			rig.wantQuiet(rig.hook("attach", "--client", "4343"))
			rig.wantRegistry(map[string]herdrClient{"4343": rig.client("w6:p3", "term_2")})
			for _, call := range rig.herdr.calls() {
				if strings.Contains(call, `"w2:p1"`) && !strings.HasPrefix(call, "pane.process_info") {
					t.Fatalf("request %q writes to the stale id w2:p1", call)
				}
			}
			if got := rig.herdr.calls(); got[len(got)-1] != reportCall("w6:p3", "idle", "") {
				t.Fatalf("requests = %q, want the stored state reported to w6:p3 last", got)
			}
		})
	}
	// tmux keeps the variables of whichever client attached last: a client whose
	// pid runs in no Herdr pane (a terminal without Herdr racing a Herdr attach)
	// is never registered under that other client's pane.
	t.Run("an attach whose client runs in no Herdr pane registers nothing and is a named error", func(t *testing.T) {
		rig := newChatRig(t)
		rig.seed(rig.registered("working", nil))
		before := rig.tmux.stored
		rig.tmux.clients = []string{"777"}
		rig.sessionEnv("w1:p1")
		got := rig.hook("attach", "--client", "777")
		if got.rc != 0 || !strings.Contains(got.stderr, "777") || !strings.Contains(got.stderr, "no Herdr pane runs") {
			t.Fatalf("rc=%d stderr=%q, want exit 0 naming the client and the cause", got.rc, got.stderr)
		}
		if rig.tmux.stored != before {
			t.Fatalf("stored = %q (was %q), want it untouched", rig.tmux.stored, before)
		}
		for _, call := range rig.herdr.calls() {
			if !strings.HasPrefix(call, "pane.list") && !strings.HasPrefix(call, "pane.process_info") {
				t.Fatalf("request %q, want only the pane lookups", call)
			}
		}
	})
	for _, verb := range []string{"attach", "sync"} {
		t.Run(verb+" without --client is a named error", func(t *testing.T) {
			rig := newChatRig(t)
			rig.sessionEnv("w1:p1")
			got := rig.hook(verb)
			if got.rc != 0 || !strings.Contains(got.stderr, verb+" needs --client") {
				t.Fatalf("rc=%d stderr=%q, want exit 0 and --client named", got.rc, got.stderr)
			}
		})
	}
}

// A detached client's pane is released and cleared; a client that was never
// registered (one without Herdr) changes nothing.
func TestHerdrStateDetachReleasesTheClientsPane(t *testing.T) {
	t.Run("a registered client: released, title cleared, registry emptied", func(t *testing.T) {
		rig := newChatRig(t)
		rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
		rig.wantQuiet(rig.hook("detach", "--client", "4242"))
		rig.wantCalls(append([]string{listCall}, releaseCalls("w1:p1")...)...)
		rig.wantRegistry(nil)
	})
	t.Run("an unregistered client changes nothing and sends nothing", func(t *testing.T) {
		rig := newChatRig(t)
		rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
		before := rig.tmux.stored
		rig.wantQuiet(rig.hook("detach", "--client", "777"))
		rig.wantCalls()
		if rig.tmux.stored != before || len(rig.tmux.setOptionCalls()) != 0 {
			t.Fatalf("stored = %q (was %q), want it untouched", rig.tmux.stored, before)
		}
	})
	// tmux gives a client-detached hook no client_pid: the hook's --client is
	// empty or absent, and the detach releases whoever tmux no longer lists.
	for name, extra := range map[string][]string{"no --client": nil, "a bare trailing --client": {"--client"}} {
		t.Run("a detach hook with "+name+" releases the vanished client only", func(t *testing.T) {
			rig := newChatRig(t)
			rig.herdr.addPane("w2:p3", "term_3", 5151)
			rig.seed(rig.registered("working", map[string]herdrClient{
				"4242": rig.client("w1:p1", "term_1"), "5151": rig.client("w2:p3", "term_3"),
			}))
			rig.tmux.clients = []string{"5151"}
			rig.wantQuiet(rig.hook("detach", extra...))
			rig.wantCalls(append([]string{listCall}, releaseCalls("w1:p1")...)...)
			rig.wantRegistry(map[string]herdrClient{"5151": rig.client("w2:p3", "term_3")})
		})
	}
	t.Run("a detach sweep with every client still attached changes nothing", func(t *testing.T) {
		rig := newChatRig(t)
		rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
		before := rig.tmux.stored
		rig.wantQuiet(rig.hook("detach"))
		rig.wantCalls()
		if rig.tmux.stored != before || len(rig.tmux.setOptionCalls()) != 0 {
			t.Fatalf("stored = %q (was %q), want it untouched", rig.tmux.stored, before)
		}
	})
	// A pane closed before the detach is the expected end of a chat in a pane:
	// nothing more is sent and no error row is written. A dial or pane.list
	// failure, or any refusal other than a pane not found, stays an error row.
	goneClient := func(t *testing.T) *chatRig {
		t.Helper()
		rig := newChatRig(t)
		rig.seed(rig.registered("working", map[string]herdrClient{"4343": rig.client("w2:p1", "term_2")}))
		return rig
	}
	t.Run("a detach whose terminal is in no pane: nothing sent, no error row, registry emptied", func(t *testing.T) {
		rig := goneClient(t)
		rig.wantQuiet(rig.hook("detach", "--client", "4343"))
		rig.wantCalls(listCall)
		rig.wantRegistry(nil)
		if logged := rig.jail.logged("cc-1"); logged != "" {
			t.Fatalf("cc-1.log = %q, want no error row for a gone pane", logged)
		}
	})
	t.Run("a sweep whose client's terminal is in no pane: nothing sent, no error row", func(t *testing.T) {
		rig := goneClient(t)
		rig.tmux.clients = nil
		rig.wantQuiet(rig.hook("detach"))
		rig.wantCalls(listCall)
		rig.wantRegistry(nil)
		if logged := rig.jail.logged("cc-1"); logged != "" {
			t.Fatalf("cc-1.log = %q, want no error row for a gone pane", logged)
		}
	})
	// pane_not_found is live Herdr 0.9.5's code, not_found the socket-api docs'.
	for _, code := range []string{"pane_not_found", "not_found"} {
		t.Run("a release Herdr refuses as "+code+": nothing more is sent, no error row", func(t *testing.T) {
			rig := newChatRig(t)
			rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
			rig.herdr.refuse["pane.release_agent"] = code
			rig.wantQuiet(rig.hook("detach", "--client", "4242"))
			rig.wantCalls(listCall, releaseCalls("w1:p1")[0])
			if logged := rig.jail.logged("cc-1"); logged != "" {
				t.Fatalf("cc-1.log = %q, want no error row for a gone pane", logged)
			}
		})
	}
	t.Run("an end event whose terminal is in no pane: nothing sent, no error row", func(t *testing.T) {
		rig := goneClient(t)
		rig.wantQuiet(rig.engine("end", `{"session_id":"s1","reason":"prompt_input_exit"}`))
		rig.wantCalls(listCall)
		if logged := rig.jail.logged("cc-1"); logged != "" {
			t.Fatalf("cc-1.log = %q, want no error row for a gone pane", logged)
		}
	})
	for name, refusal := range map[string]string{"pane.release_agent": "internal_error", "pane.list": "internal_error"} {
		t.Run("a detach whose "+name+" Herdr refuses as "+refusal+" stays an error row", func(t *testing.T) {
			rig := newChatRig(t)
			rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
			rig.herdr.refuse[name] = refusal
			got := rig.hook("detach", "--client", "4242")
			if got.rc != 0 || !strings.Contains(got.stderr, refusal) {
				t.Fatalf("rc=%d stderr=%q, want exit 0 naming the refusal", got.rc, got.stderr)
			}
			if logged := rig.jail.logged("cc-1"); !strings.Contains(logged, refusal) {
				t.Fatalf("cc-1.log = %q, want the refusal logged", logged)
			}
		})
	}
	t.Run("an unreachable Herdr socket on a detach stays an error row", func(t *testing.T) {
		rig := newChatRig(t)
		rig.seed(rig.registered("working", map[string]herdrClient{
			"4242": {Pane: "w1:p1", Terminal: "term_1", Socket: rig.herdr.path + ".gone"},
		}))
		got := rig.hook("detach", "--client", "4242")
		if got.rc != 0 || !strings.Contains(got.stderr, "list panes") {
			t.Fatalf("rc=%d stderr=%q, want exit 0 naming the failed pane.list", got.rc, got.stderr)
		}
		if logged := rig.jail.logged("cc-1"); !strings.Contains(logged, "list panes") {
			t.Fatalf("cc-1.log = %q, want the failure logged", logged)
		}
	})
	t.Run("an event after the last detach stores the state and sends nothing", func(t *testing.T) {
		rig := newChatRig(t)
		rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
		rig.wantQuiet(rig.hook("detach", "--client", "4242"))
		sent := len(rig.herdr.calls())
		rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
		if got := rig.herdr.calls(); len(got) != sent {
			t.Fatalf("requests after the detach = %q, want none", got[sent:])
		}
		if state := rig.tmux.storedState(t); state.State != "idle" {
			t.Fatalf("stored = %q, want the idle state kept for a late attach", rig.tmux.stored)
		}
	})
}

// Each attached client keeps its own pane: a client attaching from another pane
// gets its own report and leaves the other pane's alone.
func TestHerdrStateAnotherClientBindsToItsOwnPane(t *testing.T) {
	rig := newChatRig(t)
	rig.herdr.addPane("w2:p3", "term_3", 5151)
	rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
	rig.tmux.clients = []string{"4242", "5151"}
	rig.sessionEnv("w2:p3")
	rig.wantQuiet(rig.hook("attach", "--client", "5151"))
	rig.wantCalls(listCall, `pane.process_info {"pane_id":"w2:p3"}`, reportCall("w2:p3", "working", ""))
	rig.wantRegistry(map[string]herdrClient{
		"4242": rig.client("w1:p1", "term_1"),
		"5151": rig.client("w2:p3", "term_3"),
	})
	rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
	got := rig.herdr.calls()[3:]
	want := []string{listCall, reportCall("w1:p1", "idle", ""), reportCall("w2:p3", "idle", "")}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf(
			"an event after both attached =\n  %s\nwant\n  %s",
			strings.Join(got, "\n  "),
			strings.Join(want, "\n  "),
		)
	}
}

// An event with no live registered client sends nothing; a client that vanished
// without a detach hook is released at the next event.
func TestHerdrStateEventsReachOnlyLiveRegisteredClients(t *testing.T) {
	t.Run("no registered client: stored, nothing sent", func(t *testing.T) {
		rig := newChatRig(t)
		rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
		rig.wantCalls()
		if state := rig.tmux.storedState(t); state.State != "working" {
			t.Fatalf("stored = %q, want the working state", rig.tmux.stored)
		}
	})
	t.Run("a vanished client is released and forgotten, not reported to", func(t *testing.T) {
		rig := newChatRig(t)
		rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
		rig.tmux.clients = nil
		rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
		rig.wantCalls(append([]string{listCall}, releaseCalls("w1:p1")...)...)
		rig.wantRegistry(nil)
	})
	t.Run("the registry survives the end of a session", func(t *testing.T) {
		rig := newChatRig(t)
		rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
		rig.wantQuiet(rig.engine("end", `{"session_id":"s1","reason":"prompt_input_exit"}`))
		rig.wantCalls(append([]string{listCall}, releaseCalls("w1:p1")...)...)
		rig.wantRegistry(map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")})
	})
}

// The tmux hook verbs are not fired by an engine: a nested-engine guard that
// cannot read the process table never silences them.
func TestHerdrStateTmuxHookVerbsSkipTheNestedEngineGuard(t *testing.T) {
	rig := newChatRig(t)
	rig.jail.procs = map[int]fakeProc{}
	rig.seed(rig.registered("working", nil))
	rig.tmux.clients = []string{"4242"}
	rig.sessionEnv("w1:p1")
	rig.wantQuiet(rig.hook("attach", "--client", "4242"))
	rig.wantQuiet(rig.hook("sync", "--client", "4242"))
	rig.wantQuiet(rig.hook("detach", "--client", "4242"))
	rig.wantCalls(
		append(
			[]string{
				listCall, `pane.process_info {"pane_id":"w1:p1"}`, reportCall("w1:p1", "working", ""),
				listCall, listCall,
			},
			releaseCalls("w1:p1")...,
		)...,
	)
}

// A Herdr pane move keeps the terminal and assigns a new pane id: the chat keeps
// reporting to the pane its terminal is in now.
func TestHerdrStateFollowsAPaneMove(t *testing.T) {
	moved := func(t *testing.T) *chatRig {
		rig := newChatRig(t)
		rig.herdr.addPane("w2:p1", "term_2", 4343)
		rig.seed(rig.registered("idle", map[string]herdrClient{"4343": rig.client("w2:p1", "term_2")}))
		rig.tmux.chatName = "Move Me"
		rig.herdr.move("w2:p1", "w6:p3")
		return rig
	}
	t.Run("the next event lands in the new pane, which is named and titled again", func(t *testing.T) {
		rig := moved(t)
		rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
		rig.wantCalls(
			listCall, reportCall("w6:p3", "working", ""),
			`agent.list {}`, renameCall("w6:p3", "move-me"), titleCall("w6:p3", "Move Me"),
		)
		rig.namesNothingOf("w2:p1", 0)
		rig.wantRegistry(map[string]herdrClient{"4343": rig.client("w6:p3", "term_2")})
		sent := len(rig.herdr.calls())
		rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
		if got := rig.herdr.calls()[sent:]; strings.Join(got, "\n") != listCall+"\n"+reportCall("w6:p3", "idle", "") {
			t.Fatalf("an event in the settled pane = %q, want one report and no rename", got)
		}
	})
	t.Run("the vacated id given to another terminal is never sent to", func(t *testing.T) {
		rig := moved(t)
		rig.herdr.reassign("w2:p1", "term_9")
		rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
		for _, call := range rig.herdr.calls() {
			if strings.Contains(call, `"w2:p1"`) {
				t.Fatalf("request %q names the vacated id now held by another terminal", call)
			}
		}
		rig.wantRegistry(map[string]herdrClient{"4343": rig.client("w6:p3", "term_2")})
	})
	t.Run("a client that vanished after the move is released in the pane it was in", func(t *testing.T) {
		rig := moved(t)
		rig.herdr.reassign("w2:p1", "term_9")
		rig.tmux.clients = nil
		rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
		rig.wantCalls(append([]string{listCall}, releaseCalls("w6:p3")...)...)
	})
	t.Run("an end after the move releases the resolved pane", func(t *testing.T) {
		rig := moved(t)
		rig.wantQuiet(rig.engine("end", `{"session_id":"s1","reason":"prompt_input_exit"}`))
		rig.wantCalls(append([]string{listCall}, releaseCalls("w6:p3")...)...)
		rig.namesNothingOf("w2:p1", 0)
	})
	// A re-attach (attach) or the move's PTY resize (sync) of the registered
	// client re-binds it at once, without tmux copying anything.
	for _, verb := range []string{"attach", "sync"} {
		t.Run("a registered client's "+verb+" re-binds it without tmux copying anything", func(t *testing.T) {
			rig := moved(t)
			rig.tmux.env = "-HERDR_ENV\n-HERDR_PANE_ID\n-HERDR_SOCKET_PATH\n"
			rig.wantQuiet(rig.hook(verb, "--client", "4343"))
			rig.wantCalls(
				listCall, reportCall("w6:p3", "idle", ""),
				`agent.list {}`, renameCall("w6:p3", "move-me"), titleCall("w6:p3", "Move Me"),
			)
			rig.wantRegistry(map[string]herdrClient{"4343": rig.client("w6:p3", "term_2")})
		})
	}
	t.Run("an entry with no terminal binds the one of its listed pane id", func(t *testing.T) {
		rig := newChatRig(t)
		rig.seed(rig.registered("idle", map[string]herdrClient{"4242": rig.client("w1:p1", "")}))
		rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
		rig.wantCalls(listCall, reportCall("w1:p1", "working", ""))
		rig.wantRegistry(map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")})
	})
	t.Run("an entry with no terminal and a gone id binds the pane running the client pid", func(t *testing.T) {
		rig := newChatRig(t)
		rig.herdr.addPane("w6:p3", "term_2", 4343)
		rig.seed(rig.registered("idle", map[string]herdrClient{"4343": rig.client("w2:p1", "")}))
		rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
		rig.wantCalls(
			listCall, `pane.process_info {"pane_id":"w1:p1"}`, `pane.process_info {"pane_id":"w6:p3"}`,
			reportCall("w6:p3", "working", ""),
		)
		rig.wantRegistry(map[string]herdrClient{"4343": rig.client("w6:p3", "term_2")})
	})
	t.Run("a terminal in no pane: nothing sent, the terminal and cause named and logged", func(t *testing.T) {
		rig := newChatRig(t)
		rig.seed(rig.registered("idle", map[string]herdrClient{"4343": rig.client("w2:p1", "term_2")}))
		got := rig.engine("working", `{"session_id":"s1"}`)
		if got.rc != 0 || !strings.Contains(got.stderr, "term_2") || !strings.Contains(got.stderr, "in no pane") {
			t.Fatalf("rc=%d stderr=%q, want exit 0 naming the terminal and the cause", got.rc, got.stderr)
		}
		if logged := rig.jail.logged("cc-1"); !strings.Contains(logged, "term_2") {
			t.Fatalf("cc-1.log = %q, want the unresolved terminal", logged)
		}
		rig.wantCalls(listCall)
		rig.wantRegistry(map[string]herdrClient{"4343": rig.client("w2:p1", "term_2")})
	})
	t.Run("a failing pane.list: nothing sent, the cause named and logged", func(t *testing.T) {
		rig := newChatRig(t)
		rig.herdr.refuse["pane.list"] = "internal_error"
		rig.seed(rig.registered("idle", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
		got := rig.engine("working", `{"session_id":"s1"}`)
		if got.rc != 0 || !strings.Contains(got.stderr, "list panes") ||
			!strings.Contains(got.stderr, "internal_error") {
			t.Fatalf("rc=%d stderr=%q, want exit 0 naming pane.list and the refusal", got.rc, got.stderr)
		}
		if logged := rig.jail.logged("cc-1"); !strings.Contains(logged, "internal_error") {
			t.Fatalf("cc-1.log = %q, want the refusal", logged)
		}
		rig.wantCalls(listCall)
	})
}
