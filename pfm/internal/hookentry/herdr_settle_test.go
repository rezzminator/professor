package hookentry

import (
	"bytes"
	"context"
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

// nativeActivityRig is a proved native Claude seat in Herdr pane w6:p3 whose
// hooks keep their detached processes in spawned.
type nativeActivityRig struct {
	server  *fakeHerdr
	deps    herdrDeps
	lockDir string
	spawned [][]string
}

func newNativeActivityRig(t *testing.T) *nativeActivityRig {
	t.Helper()
	root := t.TempDir()
	server := newNativeReporterHerdr(t, "native-seat", "w6:p3", "native-terminal")
	server.addPane("w6:p3", "native-terminal")
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
	rig := &nativeActivityRig{server: server, deps: realHerdrDeps(), lockDir: paths.HerdrLockDir(root)}
	now := time.Unix(1000, 0)
	rig.deps.now = func() time.Time { now = now.Add(time.Millisecond); return now }
	rig.deps.spawn = func(argv []string) error { rig.spawned = append(rig.spawned, argv); return nil }
	return rig
}

// event fires one Claude hook of session activity-session: prompt,
// agent-start, agent-stop (agent a) or idle.
func (rig *nativeActivityRig) event(step string) herdrRun {
	event, payload := step, `{"session_id":"activity-session"}`
	switch step {
	case "prompt":
		event, payload = "working", `{"session_id":"activity-session","hook_event_name":"UserPromptSubmit"}`
	case "agent-start":
		payload = `{"session_id":"activity-session","agent_id":"a","hook_event_name":"SubagentStart"}`
	case "agent-stop":
		payload = `{"session_id":"activity-session","agent_id":"a","hook_event_name":"SubagentStop"}`
	}
	var stderr bytes.Buffer
	rc := reportToHerdr([]string{event}, strings.NewReader(payload), &stderr, rig.deps)
	return herdrRun{rc: rc, stderr: stderr.String()}
}

// A native report dropped at the host-wide bridge lock (another chat's
// transaction held it past the hook's deadline) has no later hook to repair a
// settled state: the last agent stop after Stop, or the Stop itself. Once the
// lock is free, the detached processes the hooks started settle the pane idle.
func TestHerdrNativeDroppedSettledStateIsResent(t *testing.T) {
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
			release := holdBridge(t, rig.lockDir)
			got := rig.event(test.dropped)
			release()
			if got.rc != 0 || !strings.Contains(got.stderr, "transaction.lock") {
				t.Fatalf(
					"%s: rc=%d stderr=%q, want exit 0 naming the held bridge lock",
					test.dropped,
					got.rc,
					got.stderr,
				)
			}
			runSpawned(t, rig.spawned, rig.deps)
			assertActivityReport(t, rig.server, "idle", "")
		})
	}
}

// A tmux chat's Stop whose @pfm_herdr write or client list timed out (a loaded
// tmux server) stored and reported nothing, and no later hook comes while the
// chat is idle. Once tmux answers, the detached process the hook started
// stores the idle state and reports it to the pane, unless a newer event fired
// meanwhile: a prompt dropped the same way leaves the pane working.
func TestHerdrTmuxDroppedStopIsResent(t *testing.T) {
	for _, test := range []struct {
		name, failing, failure, want string
		newerPrompt                  bool
	}{
		{name: "store timed out", failing: "set-option", failure: "store state", want: "idle"},
		{name: "client list timed out", failing: "list-clients", failure: "list tmux clients", want: "idle"},
		{
			name: "a newer prompt dropped too", failing: "set-option", failure: "store state", want: "working",
			newerPrompt: true,
		},
	} {
		for _, engine := range []string{"claude", "codex"} {
			t.Run(test.name+"/"+engine, func(t *testing.T) {
				rig, session := newChatRig(t), "s1"
				fire := func(event, extra string) herdrRun {
					return rig.engine(event, `{"session_id":"s1"`+extra+`}`)
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
				rig.tmux.failOn = test.failing
				rig.tmux.mu.Unlock()
				got := fire("idle", "")
				if test.newerPrompt {
					if prompt := fire("working", `,"hook_event_name":"UserPromptSubmit"`); prompt.rc != 0 {
						t.Fatalf("newer prompt: rc=%d stderr=%q", prompt.rc, prompt.stderr)
					}
				}
				rig.tmux.mu.Lock()
				rig.tmux.failOn = ""
				rig.tmux.mu.Unlock()
				if got.rc != 0 || !strings.Contains(got.stderr, test.failure) {
					t.Fatalf("idle: rc=%d stderr=%q, want exit 0 naming %q", got.rc, got.stderr, test.failure)
				}
				deps := rig.jail.deps(rig.env, &rig.clock)
				deps.tmux, deps.listPanes = rig.tmux.run, rig.tmux.listPanes
				runSpawned(t, spawned, deps)
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
		rig.wantQuiet(rig.engine("working", `{"session_id":"s1","hook_event_name":"UserPromptSubmit"}`))
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
