package hookentry

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// holdBridge takes the host-wide Herdr bridge lock the way another chat's
// slow transaction holds it, until release.
func holdBridge(t *testing.T, lockDir string) (release func()) {
	t.Helper()
	dir := filepath.Join(lockDir, "pfm-herdr-bridge")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(dir, "transaction.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("hold the bridge lock: %v", err)
	}
	return func() { _ = file.Close() }
}

// chatTranscript is an empty Claude transcript of session s1 the test owns:
// the one a main turn's hooks name for its transcript watch.
func chatTranscript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// runSpawned runs every detached process the hooks started, as each runs once
// its hook has exited; each must exit 0 and quiet.
func runSpawned(t *testing.T, spawned [][]string, deps herdrDeps) {
	t.Helper()
	for _, argv := range spawned {
		var stderr bytes.Buffer
		if rc := routeHerdrState(argv[2:], strings.NewReader(""), &stderr, deps); rc != 0 || stderr.Len() != 0 {
			t.Errorf("%v: rc=%d stderr=%s", argv, rc, &stderr)
		}
	}
}

// nativeActivityRig is a proved native Claude seat in Herdr pane w6:p3, where
// Herdr lists no Claude in front so pfm reports its state, whose hooks name its transcript
// and keep their detached processes in spawned.
type nativeActivityRig struct {
	server     *fakeHerdr
	deps       herdrDeps
	lockDir    string
	transcript string
	spawned    [][]string
}

func newNativeActivityRig(t *testing.T) *nativeActivityRig {
	t.Helper()
	root := t.TempDir()
	server := newNativeReporterHerdr(t, "native-seat", "w6:p3", "native-terminal")
	shellFrontNativeReporterPane(t, server, "w6:p3")
	t.Setenv(paths.EnvHome, root)
	t.Setenv(paths.EnvStateDB, filepath.Join(root, "pfm.db"))
	t.Setenv("TMUX", "")
	for key, value := range herdrPaneEnv(server, "w6:p3") {
		t.Setenv(key, value)
	}
	db := fleetdb.OpenSharedState(context.Background(), paths.Values{StateDB: filepath.Join(root, "pfm.db")})
	t.Cleanup(func() { _ = db.Close() })
	seat := fleetdb.Seat{
		SeatID: "native-seat", SessionID: "activity-session", Backend: "herdr", State: "live", Engine: "cc",
		HerdrSocket: server.path, HerdrPane: "w6:p3",
	}
	stampNativeReporterSeat(t, &seat)
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	rig := &nativeActivityRig{
		server: server, deps: realHerdrDeps(), lockDir: paths.HerdrLockDir(root),
		transcript: filepath.Join(root, "activity-session.jsonl"),
	}
	if err := os.WriteFile(rig.transcript, []byte(watchToolUseEntry+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	rig.deps.now = func() time.Time { now = now.Add(time.Millisecond); return now }
	rig.deps.spawn = func(argv []string) error { rig.spawned = append(rig.spawned, argv); return nil }
	return rig
}

// event fires one Claude hook of session activity-session: prompt,
// agent-start, agent-stop (agent a) or idle.
func (rig *nativeActivityRig) event(step string) herdrRun {
	event, fields := step, ""
	switch step {
	case "prompt":
		event, fields = "working", `,"hook_event_name":"UserPromptSubmit"`
	case "agent-start":
		fields = `,"agent_id":"a","hook_event_name":"SubagentStart"`
	case "agent-stop":
		fields = `,"agent_id":"a","hook_event_name":"SubagentStop"`
	}
	payload := fmt.Sprintf(`{"session_id":"activity-session","transcript_path":%q%s}`, rig.transcript, fields)
	var stderr bytes.Buffer
	rc := reportToHerdr([]string{event}, strings.NewReader(payload), &stderr, rig.deps)
	return herdrRun{rc: rc, stderr: stderr.String()}
}

// darkenHerdr makes the fake Herdr's socket take no dial, as a Herdr too loaded
// to accept one, until light; its listener serves the path again once lit.
func darkenHerdr(t *testing.T, server *fakeHerdr) (light func()) {
	t.Helper()
	dark := server.path + ".dark"
	if err := os.Rename(server.path, dark); err != nil {
		t.Fatal(err)
	}
	lit := false
	light = func() {
		if lit {
			return
		}
		lit = true
		if err := os.Rename(dark, server.path); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(light)
	return light
}

// quietly fires the native rig's steps, each of which must exit 0 and quiet.
func (rig *nativeActivityRig) quietly(t *testing.T, steps ...string) {
	t.Helper()
	for _, step := range steps {
		if got := rig.event(step); got.rc != 0 || got.stderr != "" {
			t.Fatalf("%s: rc=%d stderr=%s", step, got.rc, got.stderr)
		}
	}
}

// dropUnder fires step with fault holding, which the hook must name in its one
// fail-open line, and returns the settle process the hook started for it and
// the fault's release (a no-op once released).
func (rig *nativeActivityRig) dropUnder(t *testing.T, fault, step string) (settle []string, release func()) {
	t.Helper()
	var named string
	switch fault {
	case "bridge lock":
		release, named = holdBridge(t, rig.lockDir), "transaction.lock still held at the deadline"
	case "no dial":
		release, named = darkenHerdr(t, rig.server), "dial unix "+rig.server.path+": connect: no such file or directory"
	case "unanswered report":
		rig.server.mu.Lock()
		rig.server.unanswered = map[string]int{"pane.report_agent": 1}
		rig.server.mu.Unlock()
		release, named = func() {}, "read response to pane.report_agent"
	default:
		t.Fatalf("unknown fault %q", fault)
	}
	got := rig.event(step)
	if got.rc != 0 || strings.Count(got.stderr, "\n") != 1 || !strings.Contains(got.stderr, named) {
		t.Fatalf("%s under %s: rc=%d stderr=%q, want exit 0 and one line naming %q",
			step, fault, got.rc, got.stderr, named)
	}
	settles := spawnedVerb(rig.spawned, herdrSettle)
	if len(settles) != 1 {
		t.Fatalf("%s under %s: spawned %q, want one settle process for the dropped state", step, fault, rig.spawned)
	}
	return settles[0], release
}

// A native report dropped under host load would leave the pane on the last
// state Herdr got until the session's next hook, and a settled state (the last
// agent stop after Stop, the Stop itself) has none while the chat waits on its
// user: the host-wide bridge lock another chat's transaction held past the
// hook's deadline, a Herdr socket too loaded to take a dial (the seat proof's
// first request, then the settle's own first attempts), or a report Herdr never
// answered. The settle process the dropped hook started delivers the state once
// Herdr answers.
func TestHerdrNativeDroppedStateIsResent(t *testing.T) {
	for _, test := range []struct {
		name, fault, dropped, want, message string
		before                              []string
		darkAttempts                        int
	}{
		{
			name: "last agent stop after Stop", fault: "bridge lock", dropped: "agent-stop", want: "idle",
			before: []string{"prompt", "agent-start", "idle"},
		},
		{name: "Stop", fault: "bridge lock", dropped: "idle", want: "idle", before: []string{"prompt"}},
		{name: "Stop", fault: "no dial", dropped: "idle", want: "idle", before: []string{"prompt"}, darkAttempts: 2},
		{
			name: "prompt after Stop", fault: "no dial", dropped: "prompt", want: "working",
			before: []string{"prompt", "idle"}, darkAttempts: 2,
		},
		{
			name: "prompt after Stop", fault: "unanswered report", dropped: "prompt", want: "working",
			before: []string{"prompt", "idle"},
		},
		{
			name: "agent start after Stop", fault: "unanswered report", dropped: "agent-start", want: "working",
			message: "1 agent running", before: []string{"prompt", "idle"},
		},
	} {
		t.Run(test.fault+"/"+test.name, func(t *testing.T) {
			rig := newNativeActivityRig(t)
			rig.quietly(t, test.before...)
			settle, release := rig.dropUnder(t, test.fault, test.dropped)
			if test.darkAttempts == 0 {
				release()
			}
			waits := 0
			var stderr bytes.Buffer
			rc := settleHerdrStateWithWait(settle[3:], &stderr, rig.deps, func() {
				waits++
				if waits == test.darkAttempts {
					release()
				}
				if waits > test.darkAttempts {
					t.Fatalf("settle retried after Herdr took its dial: %s", &stderr)
				}
			})
			wantStderr := ""
			if test.darkAttempts > 0 {
				wantStderr = "pfm internal herdr-state settle (retrying): "
			}
			if rc != 0 || waits != test.darkAttempts || !strings.HasPrefix(stderr.String(), wantStderr) ||
				strings.Count(stderr.String(), "\n") != test.darkAttempts {
				t.Fatalf("settle: rc=%d waits=%d stderr=%q, want exit 0 after %d waits, "+
					"one retrying line per failed attempt", rc, waits, &stderr, test.darkAttempts)
			}
			assertActivityReport(t, rig.server, test.want, test.message)
		})
	}
}

// A newer event recorded while the settle process waits between attempts
// supersedes the state it holds: its next attempt finds the newer seq under
// the activity lock and stops without sending, so Herdr keeps the newer state.
func TestHerdrNativeSettleStopsAtANewerEvent(t *testing.T) {
	rig := newNativeActivityRig(t)
	rig.quietly(t, "prompt", "idle")
	settle, light := rig.dropUnder(t, "no dial", "prompt")
	var afterNewer []string
	waits := 0
	var stderr bytes.Buffer
	rc := settleHerdrStateWithWait(settle[3:], &stderr, rig.deps, func() {
		waits++
		if waits > 1 {
			t.Fatalf("settle retried past the newer event: %s", &stderr)
		}
		light()
		rig.quietly(t, "idle")
		afterNewer = rig.server.requests()
	})
	if rc != 0 || waits != 1 || !strings.HasPrefix(stderr.String(), "pfm internal herdr-state settle (retrying): ") ||
		strings.Count(stderr.String(), "\n") != 1 {
		t.Fatalf("settle: rc=%d waits=%d stderr=%q, want exit 0 after one failed attempt", rc, waits, &stderr)
	}
	if got := rig.server.requests(); !slices.Equal(got, afterNewer) {
		t.Errorf("Herdr requests after the newer idle:\n%q\nwant none past\n%q", got, afterNewer)
	}
	assertActivityReport(t, rig.server, "idle", "")
}

// holdActivity takes a Claude session's activity lock the way another hook
// of the same session (or its watchdog, or a settle process) holds it past
// this hook's deadline under host load, until release.
func holdActivity(t *testing.T, lockDir, session string) (release func()) {
	t.Helper()
	reporter := &herdrReporter{}
	file, err := os.OpenFile(
		filepath.Join(lockDir, reporter.activityKey(session)+".lock"), os.O_CREATE|os.O_RDWR, 0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("hold the activity lock: %v", err)
	}
	return func() { _ = file.Close() }
}

// A native hook that cannot take its session's activity lock before its
// deadline records nothing, so the settle process F12 starts for an unsent
// recorded state has nothing to re-send, and no later hook comes while the
// chat waits on its user (the agent watchdog only after its hour-long lease).
// Once the lock is free, the detached process the dropped hook started
// records the event and settles the pane idle.
func TestHerdrNativeEventDroppedAtTheActivityLockIsSettled(t *testing.T) {
	for _, test := range []struct {
		name    string
		before  []string
		dropped string
	}{
		{"last agent stop after Stop", []string{"prompt", "agent-start", "idle"}, "agent-stop"},
		{"Stop", []string{"prompt"}, "idle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rig := newNativeActivityRig(t)
			for _, step := range test.before {
				if got := rig.event(step); got.rc != 0 || got.stderr != "" {
					t.Fatalf("%s: rc=%d stderr=%s", step, got.rc, got.stderr)
				}
			}
			release := holdActivity(t, rig.lockDir, "activity-session")
			got := rig.event(test.dropped)
			release()
			if got.rc != 0 || !strings.Contains(got.stderr, "still held at the deadline") {
				t.Fatalf("%s: rc=%d stderr=%q, want exit 0 naming the held activity lock",
					test.dropped, got.rc, got.stderr)
			}
			runSpawned(t, slices.DeleteFunc(rig.spawned, func(argv []string) bool { return argv[2] != herdrSettle }),
				rig.deps)
			assertActivityReport(t, rig.server, "idle", "")
		})
	}
}

// A tmux chat's Stop whose session read (display-message) got no answer
// before the hook's deadline failed ahead of the engine proof, so it stored
// and reported nothing, and Codex fires no later hook while it waits on its
// user. The hook hands the settle process the event with the process ancestry
// the proof walks; once tmux answers, the settle reads the session, proves the
// engine and replays the event.
func TestHerdrTmuxStopDroppedBeforeTheEngineProofIsSettled(t *testing.T) {
	for _, engine := range []string{"claude", "codex"} {
		t.Run(engine, func(t *testing.T) {
			rig, session, transcript := newChatRig(t), "s1", chatTranscript(t)
			fire := func(event, extra string) herdrRun {
				return rig.engine(event, fmt.Sprintf(`{"session_id":"s1","transcript_path":%q%s}`, transcript, extra))
			}
			if engine == "codex" {
				rig, session = codexRig(t), "x1"
				rig.jail.paneEnv = map[int]map[string]string{
					codexTUI: {"TMUX": rigSocket + ",4000,0", "TMUX_PANE": "%3"},
				}
				rig.env["TMUX"], rig.env["TMUX_PANE"] = rigSocket+",94682,0", "%0"
				rig.tmux.panePID = "80"
				fire = func(event, extra string) herdrRun { return rig.codex(event, "x1", extra) }
			}
			var spawned [][]string
			rig.jail.spawn = func(argv []string) error { spawned = append(spawned, argv); return nil }
			rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
			rig.wantQuiet(fire("start", `,"source":"startup"`))
			rig.wantQuiet(fire("working", `,"hook_event_name":"UserPromptSubmit"`))
			rig.tmux.mu.Lock()
			rig.tmux.hangOn = "display-message"
			rig.tmux.mu.Unlock()
			got := fire("idle", "")
			rig.tmux.mu.Lock()
			rig.tmux.hangOn = ""
			rig.tmux.mu.Unlock()
			if got.rc != 0 || !strings.Contains(got.stderr, "read tmux session") {
				t.Fatalf("idle: rc=%d stderr=%q, want exit 0 naming the unanswered session read", got.rc, got.stderr)
			}
			deps := rig.jail.deps(rig.env, &rig.clock)
			deps.tmux, deps.listPanes = rig.tmux.run, rig.tmux.listPanes
			runSpawned(t, spawnedVerb(spawned, herdrSettle), deps)
			if stored := rig.tmux.storedState(t); stored.State != "idle" || stored.SessionID != session {
				t.Errorf("stored = %q, want session %s idle", rig.tmux.stored, session)
			}
			calls := slices.DeleteFunc(rig.herdr.calls(), func(call string) bool {
				return !strings.HasPrefix(call, "pane.report_agent")
			})
			if len(calls) == 0 || !strings.Contains(calls[len(calls)-1], `"state":"idle"`) {
				t.Errorf("herdr requests = %q, want idle the last state reported to w1:p1", calls)
			}
		})
	}
}

// A tmux chat's event whose @pfm_herdr write or client list timed out (a loaded
// tmux server) stored and reported nothing, and one whose report Herdr never
// answered left the pane on the state before it; no later hook comes while the
// chat is idle, and a prompt's is the turn's end. Once tmux and Herdr answer,
// the detached process the hook started stores the event's state and reports
// it to the pane, unless a newer event fired meanwhile: a prompt dropped the
// same way leaves the pane working.
func TestHerdrTmuxDroppedStopIsResent(t *testing.T) {
	const prompt = `,"hook_event_name":"UserPromptSubmit"`
	for _, test := range []struct {
		name, failing, unanswered, failure, want string
		newerPrompt, promptAfterStop             bool
	}{
		{name: "store timed out", failing: "set-option", failure: "store state", want: "idle"},
		{name: "client list timed out", failing: "list-clients", failure: "list tmux clients", want: "idle"},
		{
			name: "a newer prompt dropped too", failing: "set-option", failure: "store state", want: "working",
			newerPrompt: true,
		},
		{
			name: "a prompt's report unanswered", unanswered: "pane.report_agent working",
			failure: "read response to pane.report_agent", want: "working", promptAfterStop: true,
		},
	} {
		for _, engine := range []string{"claude", "codex"} {
			t.Run(test.name+"/"+engine, func(t *testing.T) {
				rig, session, transcript := newChatRig(t), "s1", chatTranscript(t)
				fire := func(event, extra string) herdrRun {
					return rig.engine(
						event,
						fmt.Sprintf(`{"session_id":"s1","transcript_path":%q%s}`, transcript, extra),
					)
				}
				if engine == "codex" {
					rig, session = codexRig(t), "x1"
					rig.jail.paneEnv = map[int]map[string]string{
						codexTUI: {"TMUX": rigSocket + ",4000,0", "TMUX_PANE": "%3"},
					}
					rig.env["TMUX"], rig.env["TMUX_PANE"] = rigSocket+",94682,0", "%0"
					rig.tmux.panePID = "80"
					fire = func(event, extra string) herdrRun { return rig.codex(event, "x1", extra) }
				}
				var spawned [][]string
				rig.jail.spawn = func(argv []string) error { spawned = append(spawned, argv); return nil }
				rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
				rig.wantQuiet(fire("start", `,"source":"startup"`))
				rig.wantQuiet(fire("working", prompt))
				dropped, extra := "idle", ""
				if test.promptAfterStop {
					rig.wantQuiet(fire("idle", ""))
					dropped, extra = "working", prompt
				}
				rig.tmux.mu.Lock()
				rig.tmux.failOn = test.failing
				rig.tmux.mu.Unlock()
				rig.herdr.mu.Lock()
				rig.herdr.unanswered = map[string]int{test.unanswered: 1}
				rig.herdr.mu.Unlock()
				got := fire(dropped, extra)
				if test.newerPrompt {
					if prompt := fire("working", prompt); prompt.rc != 0 {
						t.Fatalf("newer prompt: rc=%d stderr=%q", prompt.rc, prompt.stderr)
					}
				}
				rig.tmux.mu.Lock()
				rig.tmux.failOn = ""
				rig.tmux.mu.Unlock()
				if got.rc != 0 || !strings.Contains(got.stderr, test.failure) {
					t.Fatalf("%s: rc=%d stderr=%q, want exit 0 naming %q", dropped, got.rc, got.stderr, test.failure)
				}
				deps := rig.jail.deps(rig.env, &rig.clock)
				deps.tmux, deps.listPanes = rig.tmux.run, rig.tmux.listPanes
				runSpawned(t, spawnedVerb(spawned, herdrSettle), deps)
				if stored := rig.tmux.storedState(t); stored.State != test.want || stored.SessionID != session {
					t.Errorf("stored = %q, want session %s %s", rig.tmux.stored, session, test.want)
				}
				calls := slices.DeleteFunc(rig.herdr.calls(), func(call string) bool {
					return !strings.HasPrefix(call, "pane.report_agent")
				})
				if len(calls) == 0 || !strings.Contains(calls[len(calls)-1], `"state":"`+test.want+`"`) {
					t.Errorf("herdr requests = %q, want %s the last state reported to w1:p1", calls, test.want)
				}
			})
		}
	}
}

// A Herdr refusal is Herdr's answer, and a session name no lock file can carry
// stays one, not a host too loaded to ask: neither starts a settle process from
// a tmux hook, and a native settle that meets a refusal stops after a single
// attempt, naming it.
func TestHerdrPermanentFailureIsNotRetried(t *testing.T) {
	t.Run("tmux session no lock can name", func(t *testing.T) {
		rig := newChatRig(t)
		rig.tmux.session = "team/one"
		var spawned [][]string
		rig.jail.spawn = func(argv []string) error { spawned = append(spawned, argv); return nil }
		got := rig.engine("idle", `{"session_id":"s1"}`)
		if got.rc != 0 || !strings.Contains(got.stderr, "not a plain session name") {
			t.Fatalf("idle: rc=%d stderr=%q, want exit 0 naming the unlockable session", got.rc, got.stderr)
		}
		for _, argv := range spawned {
			if slices.Contains(argv, herdrSettle) {
				t.Errorf("spawned %q for an unlockable session, want no settle process", argv)
			}
		}
	})
	t.Run("tmux hook", func(t *testing.T) {
		rig := newChatRig(t)
		var spawned [][]string
		rig.jail.spawn = func(argv []string) error { spawned = append(spawned, argv); return nil }
		rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
		rig.wantQuiet(rig.engine("working", fmt.Sprintf(
			`{"session_id":"s1","hook_event_name":"UserPromptSubmit","transcript_path":%q}`, chatTranscript(t))))
		rig.herdr.mu.Lock()
		rig.herdr.refuse["pane.report_agent idle"] = "pane_not_found"
		rig.herdr.mu.Unlock()
		got := rig.engine("idle", `{"session_id":"s1"}`)
		if got.rc != 0 || !strings.Contains(got.stderr, "pane_not_found") {
			t.Fatalf("idle: rc=%d stderr=%q, want exit 0 naming the refusal", got.rc, got.stderr)
		}
		for _, argv := range spawned {
			if slices.Contains(argv, herdrSettle) {
				t.Errorf("spawned %q for a refused report, want no settle process", argv)
			}
		}
	})
	t.Run("native settle", func(t *testing.T) {
		rig := newNativeActivityRig(t)
		if got := rig.event("prompt"); got.rc != 0 || got.stderr != "" {
			t.Fatalf("prompt: rc=%d stderr=%s", got.rc, got.stderr)
		}
		release := holdBridge(t, rig.lockDir)
		dropped := rig.event("idle")
		release()
		if dropped.rc != 0 {
			t.Fatalf("idle: rc=%d stderr=%s", dropped.rc, dropped.stderr)
		}
		var settle []string
		for _, argv := range rig.spawned {
			if slices.Contains(argv, herdrSettle) {
				settle = argv
			}
		}
		if settle == nil {
			t.Fatalf("spawned %q, want a settle process for the dropped idle", rig.spawned)
		}
		rig.server.mu.Lock()
		rig.server.refuse = map[string]string{"pane.report_agent": "pane_not_found"}
		rig.server.mu.Unlock()
		var stderr bytes.Buffer
		rc := settleHerdrStateWithWait(settle[3:], &stderr, rig.deps, func() {
			t.Fatalf("settle retried a refused report: %s", &stderr)
		})
		if rc != 0 || !strings.Contains(stderr.String(), "pane_not_found") {
			t.Fatalf("settle: rc=%d stderr=%q, want exit 0 naming the refusal", rc, &stderr)
		}
	})
}
