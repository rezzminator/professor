package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// A native Claude pane Herdr detects is Herdr's own: its screen detection reads
// the state and its own Claude integration reports the session, so pfm sends it
// nothing, while the seat still binds each session the pane runs.
func TestNativeClaudeDetectedPaneIsHerdrs(t *testing.T) {
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
	bound := func() string {
		db := fleetdb.OpenSharedState(context.Background(), values)
		current, found, err := db.Seat(context.Background(), seat.SeatID)
		if closeErr := db.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err != nil || !found {
			t.Fatalf("seat %s in %s: found=%v err=%v", seat.SeatID, values.StateDB, found, err)
		}
		return current.SessionID
	}
	reporter := &herdrReporter{deps: deps}
	for _, hook := range []struct {
		event, payload, session string
		unread                  bool
	}{
		{"start", `{"session_id":"s1","source":"startup","transcript_path":"/fixture/session.jsonl"}`, "s1", false},
		// The foreground read after the seat's proof fails: Herdr may not see
		// Claude, so the state goes out under pfm's own source.
		{"working", `{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"go"}`, "s1", true},
		// Herdr sees Claude again: pfm releases that report, which would
		// outrank Herdr's screen detection.
		{"blocked", `{"session_id":"s1","hook_event_name":"PermissionRequest","tool_name":"Bash"}`, "s1", false},
		{"idle", `{"session_id":"s1"}`, "s1", false},
		{"start", `{"session_id":"s2","source":"clear"}`, "s2", false},
		{"start", `{"session_id":"s3","source":"compact"}`, "s3", false},
		{"start", `{"session_id":"s4","source":"fork"}`, "s4", false},
		{"start", `{"session_id":"s4","source":"resume"}`, "s4", false},
	} {
		wantWarning := ""
		if hook.unread {
			server.mu.Lock()
			server.processInfoAnswers = server.processInfoSeen + 1
			server.mu.Unlock()
			wantWarning = "pfm internal herdr-state working: native Claude seat native-seat: read pane w6:p3 " +
				"foreground (any report goes under pfm's own source): herdr refused pane.process_info pfm-seat " +
				"pane w6:p3: server_error: refused by fake herdr\n"
		}
		warning := run(hook.event, hook.payload)
		server.mu.Lock()
		server.processInfoAnswers = 0
		server.mu.Unlock()
		if warning != wantWarning {
			t.Fatalf("%s %s: warning %q, want %q", hook.event, hook.payload, warning, wantWarning)
		}
		if got := bound(); got != hook.session {
			t.Fatalf("%s %s: seat bound to %q, want %q", hook.event, hook.payload, got, hook.session)
		}
		if hook.event == "blocked" {
			state, err := reporter.readActivity("s1")
			if err != nil || len(state.Targets) != 0 || len(state.RetiredTargets) != 0 {
				t.Fatalf("record %s = %+v (err %v), want the released pane gone from it",
					reporter.activityPath("s1"), state, err)
			}
		}
	}
	// A delayed old start must not rebind the durable session.
	currentNow := now
	now = 1
	if warning := run("start", `{"session_id":"stale","source":"resume"}`); !strings.Contains(warning, "predates") {
		t.Fatalf("stale start accepted: %s", warning)
	}
	now = currentNow
	if got := bound(); got != "s4" {
		t.Fatalf("stale start rebound the seat to %q, want s4", got)
	}
	if warning := run("end", `{"session_id":"s4","reason":"prompt_input_exit"}`); warning != "" {
		t.Fatal(warning)
	}
	want := []string{
		`pane.report_agent {"agent":"claude","agent_session_id":"s1","pane_id":"w6:p3",` +
			`"resume_argv":["` + herdr.ResumeCommand + `","chat","open","--restored","s1"],"source":"pfm","state":"working"}`,
		`pane.release_agent {"agent":"claude","pane_id":"w6:p3","source":"pfm"}`,
		// The end leaves the agent to Herdr and takes pfm's title off the pane.
		`pane.report_metadata {"clear_title":true,"pane_id":"w6:p3","source":"pfm"}`,
	}
	if calls := server.calls(); !slices.Equal(calls, want) {
		t.Fatalf("Herdr received\n%s\nwant\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
	if requests := herdrRequests(t, server); len(requests) != 3 ||
		seqOf(t, requests[1]) <= seqOf(t, requests[0]) {
		t.Fatalf("requests %q: want the release strictly newer than the report", requests)
	}
}

// seqOf is the seq a herdrRequests line ends with.
func seqOf(t *testing.T, request string) uint64 {
	t.Helper()
	_, field, found := strings.Cut(request, " seq=")
	seq, err := strconv.ParseUint(field, 10, 64)
	if !found || err != nil {
		t.Fatalf("request %q: no seq (%v)", request, err)
	}
	return seq
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
	deps.tmux, deps.listPanes = rig.tmux.run, rig.tmux.listPanes
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
	var native []string
	for _, request := range herdrRequests(t, rig.herdr)[before:] {
		if strings.Contains(request, " pane=w2:p2 ") {
			native = append(native, request)
		}
	}
	// The old worker's replay would go out at seq 11: the release is newer, and
	// no report follows it.
	if want := []string{"pane.release_agent pane=w2:p2 source=pfm agent=claude seq=12"}; !slices.Equal(native, want) {
		t.Fatalf("native pane requests = %q, want %q", native, want)
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
	if len(lines) != 2 || !strings.Contains(lines[1], `"method":"pane.release_agent"`) ||
		!strings.Contains(lines[1], `"seq":12`) {
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
	var warned bytes.Buffer
	reporter.stderr = &warned
	if done, err := reporter.expireAgents("s1"); !done || err != nil {
		t.Fatalf("malformed record: done=%v err=%v, want the watchdog to finish on an empty state", done, err)
	}
	wantWarning := "pfm internal herdr-state : decode Claude activity " + reporter.activityPath("s1") +
		" (starting empty): unexpected end of JSON input\n"
	if warned.String() != wantWarning {
		t.Fatalf("malformed record warning = %q, want %q", warned.String(), wantWarning)
	}
}

// nativeOwner is the pane owner record an older build's native seat reservation left.
const nativeOwner = "native\x00seat-1\x00native-terminal\x00900001\x0010\x0011\x00claude\x00w1:p1\x00w1"

// herdrRequests renders each request the fake took as its method and the
// params it set, seq included.
func herdrRequests(t *testing.T, server *fakeHerdr) []string {
	t.Helper()
	var got []string
	for _, line := range server.requests() {
		var request struct {
			Method string `json:"method"`
			Params struct {
				Pane    string   `json:"pane_id"`
				Source  string   `json:"source"`
				Agent   string   `json:"agent"`
				Session string   `json:"agent_session_id"`
				Resume  []string `json:"resume_argv"`
				Seq     uint64   `json:"seq"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatalf("decode request %q: %v", line, err)
		}
		params := request.Params
		parts := []string{request.Method}
		for _, field := range [][2]string{
			{"pane", params.Pane},
			{"source", params.Source},
			{"agent", params.Agent},
			{"session", params.Session},
			{"resume", strings.Join(params.Resume, ",")},
		} {
			if field[1] != "" {
				parts = append(parts, field[0]+"="+field[1])
			}
		}
		if params.Seq != 0 {
			parts = append(parts, fmt.Sprintf("seq=%d", params.Seq))
		}
		got = append(got, strings.Join(parts, " "))
	}
	return got
}
