package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestNativeClaudeSessionLifecycleMetadata(t *testing.T) {
	server := newNativeReporterHerdr(t, "native-seat", "w6:p3", "native-terminal")
	values := paths.Values{StateDB: t.TempDir() + "/state.db"}
	seat := fleetdb.Seat{
		SeatID:      "native-seat",
		SessionID:   "old",
		Backend:     "herdr",
		State:       "live",
		Engine:      "cc",
		HerdrSocket: server.path,
		HerdrPane:   "w6:p3",
	}
	stampNativeReporterSeat(t, &seat)
	seatStateSQL(t, values, seat)
	env := herdrPaneEnv(server, "w6:p3")
	jail := newHerdrJail(t)
	now := int64(time.Second)
	deps := jail.deps(env, &now)
	deps.seatPaths = &values
	deps.getpid = os.Getpid
	deps.getenv = func(key string) string {
		if key == paths.EnvProcRoot {
			return os.Getenv(key)
		}
		return env[key]
	}
	run := func(event, payload string) string {
		var stderr bytes.Buffer
		if rc := reportToHerdr([]string{event}, strings.NewReader(payload), &stderr, deps); rc != 0 {
			t.Fatalf("hook rc=%d stderr=%s", rc, &stderr)
		}
		return stderr.String()
	}
	for _, source := range []string{"startup", "resume", "clear", "compact", "fork"} {
		if warning := run(
			"start",
			`{"session_id":"s1","source":"`+source+`","transcript_path":"/fixture/session.jsonl"}`,
		); warning != "" {
			t.Fatal(warning)
		}
		lines := server.requests()
		line := lines[len(lines)-1]
		if !strings.Contains(line, `"method":"pane.report_agent_session"`) ||
			!strings.Contains(line, `"source":"herdr:claude"`) ||
			!strings.Contains(line, `"agent_session_path":"/fixture/session.jsonl"`) ||
			!strings.Contains(line, `"session_start_source":"`+source+`"`) ||
			!strings.Contains(line, `"--restored"`) {
			t.Fatalf("incomplete native session: %s", line)
		}
	}
	before := len(server.requests())
	for _, event := range []string{"working", "idle", "blocked", "notification", "agent-start", "agent-stop"} {
		payload := `{"session_id":"s1","notification_type":"permission_prompt","hook_event_name":"PostToolUse"}`
		if event == "agent-start" {
			payload = `{"session_id":"s1","agent_id":"a","hook_event_name":"SubagentStart"}`
		}
		if event == "agent-stop" {
			payload = `{"session_id":"s1","agent_id":"a","hook_event_name":"SubagentStop"}`
		}
		if warning := run(event, payload); warning != "" {
			t.Fatal(warning)
		}
	}
	if len(server.requests()) != before {
		t.Fatalf("warm native lifecycle mutated Herdr: %q", server.requests()[before:])
	}
	// A delayed old start must not rebind the durable session before rejection.
	currentNow := now
	now = 1
	if warning := run("start", `{"session_id":"stale","source":"resume"}`); !strings.Contains(warning, "predates") {
		t.Fatalf("stale start accepted: %s", warning)
	}
	now = currentNow
	db := fleetdb.OpenSharedState(context.Background(), values)
	current, found, err := db.Seat(context.Background(), seat.SeatID)
	if closeErr := db.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil || !found || current.SessionID != "s1" {
		t.Fatalf("stale start rebound seat: %+v found=%v err=%v", current, found, err)
	}
	if warning := run("end", `{"session_id":"s1","reason":"prompt_input_exit"}`); warning != "" {
		t.Fatal(warning)
	}
	lines := server.requests()
	if !strings.Contains(lines[len(lines)-2], `"method":"pane.release_agent"`) ||
		!strings.Contains(lines[len(lines)-1], `"clear_title":true`) {
		t.Fatalf("native end lost clearing: %q", lines)
	}
	for _, line := range lines {
		if strings.Contains(line, `"method":"pane.report_agent"`) {
			t.Fatalf("native session claimed state: %s", line)
		}
	}
}

func TestNativeClaudeSessionWaitsForDetectionWithoutLosingRestore(t *testing.T) {
	for _, refused := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "sampler catches up", true: "deadline leaves retryable owner"}[refused],
			func(t *testing.T) {
				server := newFakeHerdr(t)
				server.sessionResumeRefusals = 1
				if refused {
					server.refuse["pane.report_agent_session"] = "resume_not_accepted"
				}
				jail := newHerdrJail(t)
				now := int64(time.Second)
				reporter := &herdrReporter{
					deps:             jail.deps(herdrPaneEnv(server, "w1:p1"), &now),
					nativeSeatReport: true,
					seq:              10,
					session:          "native-owner",
				}
				if err := os.MkdirAll(jail.lockDir, 0o700); err != nil {
					t.Fatal(err)
				}
				target := herdrTarget{pane: "w1:p1", socket: server.path}
				if err := reporter.reservePane(target); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
				defer cancel()
				err := reporter.deferNativeClaude(ctx, target, herdrPayload{SessionID: "s1", Source: "startup"}, 10)
				if refused != (err != nil) {
					t.Fatalf("refused=%v error=%v", refused, err)
				}
				owner, ownerErr := reporter.paneOwner(target)
				if ownerErr != nil || owner.NativeDetection == refused {
					t.Fatalf("migration marker=%+v err=%v", owner, ownerErr)
				}
				sessions := 0
				for _, line := range server.requests() {
					if strings.Contains(line, `"method":"pane.report_agent"`) {
						t.Fatalf("sampler race reclaimed lifecycle state: %s", line)
					}
					if strings.Contains(line, `"method":"pane.report_agent_session"`) {
						sessions++
						if !strings.Contains(line, `"source":"herdr:claude"`) ||
							!strings.Contains(line, `"resume_argv"`) {
							t.Fatalf("restore lost on retry: %s", line)
						}
					}
				}
				if sessions < 2 {
					t.Fatalf("native detection refusal was not retried: %q", server.requests())
				}
				if refused {
					if !strings.Contains(err.Error(), "resume_not_accepted") ||
						!strings.Contains(err.Error(), "deadline") {
						t.Fatalf("restore failure hidden: %v", err)
					}
					server.mu.Lock()
					delete(server.refuse, "pane.report_agent_session")
					server.mu.Unlock()
					if err := reporter.deferNativeClaude(
						context.Background(),
						target,
						herdrPayload{SessionID: "s1"},
						20,
					); err != nil {
						t.Fatal(err)
					}
					owner, err := reporter.paneOwner(target)
					if err != nil || !owner.NativeDetection {
						t.Fatalf("later hook failed to finish migration: %+v %v", owner, err)
					}
				}
			},
		)
	}
}

func TestNativeReplayTransportProof(t *testing.T) {
	state := herdrActivity{TmuxSocket: "tmux-socket", TmuxSession: "chat"}
	for _, tc := range []struct {
		name, owner, terminal string
		native, want          bool
	}{
		{"registered tmux", "tmux-socket\x00chat\x0042", "term", false, true},
		{"native bool", "tmux-socket\x00chat\x0042", "term", true, false},
		{"historical direct", "", "", false, false},
		{"missing owner", "", "term", false, false},
		{"native owner", "native\x00seat\x00term", "term", false, false},
		{"wrong socket", "other\x00chat\x0042", "term", false, false},
		{"wrong session", "tmux-socket\x00other\x0042", "term", false, false},
		{"missing terminal", "tmux-socket\x00chat\x0042", "", false, false},
		{"bad client", "tmux-socket\x00chat\x00oops", "term", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := state.tmuxTarget(herdrActivityTarget{Owner: tc.owner, Terminal: tc.terminal, Native: tc.native})
			if got != tc.want {
				t.Fatalf("tmux proof=%v want %v", got, tc.want)
			}
		})
	}
}

// Reproduce the old executable's durable reread and send loop. It intentionally
// does not call any new migration helper: the removed Targets fence it.
func baselineNativeReplay(t *testing.T, reporter *herdrReporter, session string) {
	t.Helper()
	ctx := context.Background()
	unlock, err := reporter.lock(ctx, reporter.activityKey(session))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	state, err := reporter.readActivity(session)
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := reporter.lockBridge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge()
	for _, saved := range state.Targets {
		target := herdrTarget{socket: saved.Socket, pane: saved.Pane, terminal: saved.Terminal}
		owner, err := reporter.paneOwner(target)
		if err != nil {
			t.Fatal(err)
		}
		if owner.Client != saved.Owner {
			continue
		}
		reporter.seq, reporter.nativeSeatReport = saved.Seq, saved.Native
		if err := reporter.send(ctx, target, &herdrAction{stored: state.Stored}, saved.Seq+1); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeRetirementFencesBaselineWorkerAndPreservesTmux(t *testing.T) {
	rig := newChatRig(t)
	rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
	rig.wantQuiet(rig.engine("agent-start", `{"session_id":"s1","agent_id":"a","hook_event_name":"SubagentStart"}`))
	rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
	deps := rig.jail.deps(herdrPaneEnv(rig.herdr, "w2:p2"), &rig.clock)
	deps.tmux = rig.tmux.run
	reporter := &herdrReporter{deps: deps, options: herdrOptions{engine: pfmengine.Claude, event: herdr.EventIdle}}
	state, err := reporter.readActivity("s1")
	if err != nil {
		t.Fatal(err)
	}
	tmuxTarget := state.Targets[0]
	// The historical record omits Native entirely and has no terminal.
	state.Targets = append(state.Targets, herdrActivityTarget{Pane: "w2:p2", Socket: rig.herdr.path, Seq: 10})
	if err := reporter.writeActivity("s1", state); err != nil {
		t.Fatal(err)
	}
	before := len(rig.herdr.requests())
	if err := reporter.runActivity(context.Background(), herdrPayload{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	got, err := reporter.readActivity("s1")
	if err != nil || len(got.Targets) != 1 || got.Targets[0] != tmuxTarget || len(got.Stored.Agents) != 1 ||
		got.Stored.MainStoppedAt == 0 {
		t.Fatalf("mixed activity lost tmux state: %+v err=%v", got, err)
	}
	baselineNativeReplay(t, reporter, "s1")
	var releaseSeq, sessionSeq uint64
	for _, line := range rig.herdr.requests()[before:] {
		var req struct {
			Method string `json:"method"`
			Params struct {
				Pane    string   `json:"pane_id"`
				Seq     uint64   `json:"seq"`
				Session string   `json:"agent_session_id"`
				Resume  []string `json:"resume_argv"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			t.Fatal(err)
		}
		if req.Params.Pane != "w2:p2" {
			continue
		}
		switch req.Method {
		case "pane.report_agent":
			t.Fatalf("baseline worker reasserted native state: %s", line)
		case "pane.release_agent":
			releaseSeq = req.Params.Seq
		case "pane.report_agent_session":
			sessionSeq = req.Params.Seq
			if req.Params.Session != "s1" || strings.Join(req.Params.Resume, " ") != "pfm chat open --restored s1" {
				t.Fatalf("lost restore metadata: %s", line)
			}
		}
	}
	if releaseSeq <= 11 || sessionSeq <= releaseSeq {
		t.Fatalf("release/session ordering: %d %d", releaseSeq, sessionSeq)
	}
	// The real current watchdog must also keep the proved tmux expiry path.
	rig.clock += int64(time.Hour + time.Second)
	if done, err := reporter.expireAgents("s1"); err != nil || !done {
		t.Fatalf("tmux expiry: done=%v err=%v", done, err)
	}
	assertActivityReport(t, rig.herdr, "idle", "")
}

func TestNativeRetirementSerializesAfterInflightBaselinePublication(t *testing.T) {
	server := newFakeHerdr(t)
	jail := newHerdrJail(t)
	now := int64(time.Second)
	deps := jail.deps(herdrPaneEnv(server, "w1:p1"), &now)
	old := &herdrReporter{deps: deps, seq: 10, nativeSeatReport: true}
	migration := &herdrReporter{deps: deps}
	if err := os.MkdirAll(jail.lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	state := herdrActivity{
		Stored:  herdrStored{Engine: "claude", SessionID: "s1", State: "working"},
		Targets: []herdrActivityTarget{{Pane: "w1:p1", Socket: server.path, Seq: 10, Native: true}},
	}
	if err := old.writeActivity("s1", state); err != nil {
		t.Fatal(err)
	}
	// An old executable has already read the target under the activity lock.
	// Start migration while that publication is still allowed to finish.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := old.lock(ctx, old.activityKey("s1"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	started, finished := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := migration.expireAgents("s1")
		finished <- err
	}()
	<-started
	bridge, err := old.lockBridge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = old.send(ctx, herdrTarget{pane: "w1:p1", socket: server.path}, &herdrAction{stored: state.Stored}, 11)
	bridge()
	if err != nil {
		t.Fatal(err)
	}
	lines := server.requests()
	if len(lines) != 1 || !strings.Contains(lines[0], `"method":"pane.report_agent"`) {
		t.Fatalf("migration crossed in-flight publication: %q", lines)
	}
	unlock()
	unlock = nil
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("migration did not finish after old publication unlocked")
	}
	lines = server.requests()
	if len(lines) != 3 || !strings.Contains(lines[1], `"method":"pane.release_agent"`) ||
		!strings.Contains(lines[1], `"seq":12`) || !strings.Contains(lines[2], `"method":"pane.report_agent_session"`) {
		t.Fatalf("release did not settle after in-flight old send: %q", lines)
	}
	baselineNativeReplay(t, old, "s1")
	if got := server.requests(); len(got) != len(lines) {
		t.Fatalf("old worker replayed after serialized release: %q", got)
	}
}

func TestNativeRetirementOwnershipAndMovedTerminal(t *testing.T) {
	for _, mode := range []string{"matching", "replacement", "newer", "moved", "gone"} {
		t.Run(mode, func(t *testing.T) {
			server := newFakeHerdr(t)
			server.addPane("w1:p1", "native-terminal")
			if mode == "moved" || mode == "gone" {
				server.mu.Lock()
				if mode == "gone" {
					server.panes = nil
				} else {
					server.panes[0].id = "w2:p2"
				}
				server.mu.Unlock()
			}
			jail := newHerdrJail(t)
			now := int64(time.Second)
			reporter := &herdrReporter{
				deps:    jail.deps(herdrPaneEnv(server, "w1:p1"), &now),
				seq:     10,
				session: "native-owner",
			}
			target := herdrTarget{pane: "w1:p1", socket: server.path, terminal: "native-terminal"}
			if err := os.MkdirAll(jail.lockDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := reporter.reservePane(target); err != nil {
				t.Fatal(err)
			}
			owner, err := reporter.paneOwner(target)
			if err != nil {
				t.Fatal(err)
			}
			state := herdrActivity{
				Stored: herdrStored{Engine: "claude", SessionID: "s1"},
				Targets: []herdrActivityTarget{
					{
						Pane:     target.pane,
						Socket:   target.socket,
						Terminal: target.terminal,
						Owner:    owner.Client,
						Seq:      10,
						Native:   true,
					},
				},
			}
			if mode == "replacement" {
				reporter.session = "replacement"
			}
			if mode == "replacement" || mode == "newer" {
				reporter.seq = 20
				if err := reporter.reservePane(target); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(reporter.paneOwnerPath(target))
			if err != nil {
				t.Fatal(err)
			}
			if err := reporter.writeActivity("s1", state); err != nil {
				t.Fatal(err)
			}
			if _, err := reporter.expireAgents("s1"); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(reporter.paneOwnerPath(target))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("migration changed ownership: %s -> %s err=%v", before, after, err)
			}
			lines := server.requests()
			if mode == "gone" {
				state, err := reporter.readActivity("s1")
				if err != nil || len(state.RetiredTargets) != 0 || len(state.Targets) != 0 {
					t.Fatalf("vanished terminal retirement incomplete: %+v %v", state, err)
				}
				return
			}
			if mode == "replacement" || mode == "newer" {
				if len(lines) != 0 {
					t.Fatalf("new owner mutated: %q", lines)
				}
				return
			}
			wantPane := "w1:p1"
			if mode == "moved" {
				wantPane = "w2:p2"
			}
			found := false
			for _, line := range lines {
				if strings.Contains(line, `"method":"pane.release_agent"`) &&
					strings.Contains(line, `"pane_id":"`+wantPane+`"`) {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing native release: %q", lines)
			}
		})
	}
}

func TestNativeRetirementFailureStillAdvancesHealthyTmux(t *testing.T) {
	rig := newChatRig(t)
	rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
	rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
	reporter := &herdrReporter{deps: rig.jail.deps(activityEnv(rig.herdr), &rig.clock)}
	state, err := reporter.readActivity("s1")
	if err != nil {
		t.Fatal(err)
	}
	native := newFakeHerdr(t)
	native.refuse["pane.release_agent"] = "fixture retirement refused"
	state.Targets = append(state.Targets, herdrActivityTarget{Pane: "native", Socket: native.path, Seq: 10})
	if err := reporter.writeActivity("s1", state); err != nil {
		t.Fatal(err)
	}
	got := rig.engine("idle", `{"session_id":"s1"}`)
	if !strings.Contains(got.stderr, "fixture retirement refused") {
		t.Fatalf("pending release hidden: %+v", got)
	}
	assertActivityReport(t, rig.herdr, "idle", "")
	state, err = reporter.readActivity("s1")
	if err != nil || len(state.RetiredTargets) != 1 || len(state.Targets) != 1 {
		t.Fatalf("lost safe replay/retry state: %+v %v", state, err)
	}
}

func TestNativeRetirementRetriesAndNamesRecordErrors(t *testing.T) {
	server := newFakeHerdr(t)
	jail := newHerdrJail(t)
	now := int64(time.Second)
	reporter := &herdrReporter{deps: jail.deps(herdrPaneEnv(server, "w1:p1"), &now)}
	if err := os.MkdirAll(jail.lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	state := herdrActivity{
		Stored:  herdrStored{Engine: "claude", SessionID: "s1"},
		Targets: []herdrActivityTarget{{Pane: "w1:p1", Socket: server.path, Seq: 10}},
	}
	if err := reporter.writeActivity("s1", state); err != nil {
		t.Fatal(err)
	}
	server.refuse["pane.release_agent"] = "refused"
	if _, err := reporter.expireAgents("s1"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("failed release hidden: %v", err)
	}
	got, err := reporter.readActivity("s1")
	if err != nil || len(got.Targets) != 0 || len(got.RetiredTargets) != 1 {
		t.Fatalf("unsafe retry state: %+v %v", got, err)
	}
	delete(server.refuse, "pane.release_agent")
	if _, err := reporter.expireAgents("s1"); err != nil {
		t.Fatal(err)
	}
	got, err = reporter.readActivity("s1")
	if err != nil || len(got.RetiredTargets) != 0 {
		t.Fatalf("release not retried: %+v %v", got, err)
	}
	if err := os.WriteFile(reporter.activityPath("s1"), []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reporter.expireAgents("s1"); err == nil || !strings.Contains(err.Error(), "decode Claude activity") {
		t.Fatalf("malformed record read as absent: %v", err)
	}
}
