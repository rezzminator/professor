package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Historical native=false targets are not proof of a tmux transport.
func TestHerdrNativeReplayRetirement(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(strconv.FormatBool(native), func(t *testing.T) {
			server := newFakeHerdr(t)
			jail := newHerdrJail(t)
			now := int64(time.Hour * 2)
			deps := jail.deps(herdrPaneEnv(server, "w1:p1"), &now)
			reporter := &herdrReporter{deps: deps}
			state := herdrActivity{
				Stored: herdrStored{
					Engine:        "claude",
					SessionID:     "s1",
					State:         "working",
					Agents:        map[string]bool{"a": true},
					MainStoppedAt: 1,
				},
				Targets: []herdrActivityTarget{{Pane: "w1:p1", Socket: server.path, Seq: 10, Native: native}},
			}
			if err := os.MkdirAll(jail.lockDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := reporter.writeActivity("s1", state); err != nil {
				t.Fatal(err)
			}
			done, err := reporter.expireAgents("s1")
			if err != nil || !done {
				t.Fatalf("retire: done=%v err=%v", done, err)
			}
			for _, line := range server.requests() {
				if strings.Contains(line, `"method":"pane.report_agent"`) {
					t.Fatalf("native watchdog reasserted state: %s", line)
				}
			}
			got, err := reporter.readActivity("s1")
			if err != nil || len(got.Targets) != 0 {
				t.Fatalf("baseline worker can still replay targets: %+v err=%v", got, err)
			}
		})
	}
}

func TestHerdrBackgroundAgents(t *testing.T) {
	tests := []struct {
		name    string
		events  []string
		want    string
		message string
	}{
		{"Stop with one agent", []string{"prompt", "agent-start:a", "idle"}, "working", "1 agent running"},
		{"last stop after main Stop", []string{"prompt", "agent-start:a", "idle", "agent-stop:a"}, "idle", ""},
		{"automatic main resume", []string{"prompt", "agent-start:a", "idle", "tool", "agent-stop:a"}, "working", ""},
		{"last stop during main turn", []string{"prompt", "agent-start:a", "agent-stop:a"}, "working", ""},
		{
			"new prompt stays working",
			[]string{"prompt", "agent-start:a", "idle", "prompt", "agent-stop:a"},
			"working",
			"",
		},
		{"clear resets", []string{"prompt", "agent-start:a", "start:clear", "idle"}, "idle", ""},
		{"resume resets", []string{"prompt", "agent-start:a", "start:resume", "idle"}, "idle", ""},
		{
			"compact preserves",
			[]string{"prompt", "agent-start:a", "start:compact", "idle"},
			"working",
			"1 agent running",
		},
		{
			"duplicate and unknown stops",
			[]string{
				"prompt",
				"agent-start:a",
				"agent-start:a",
				"agent-start:b",
				"idle",
				"agent-stop:missing",
				"agent-stop:a",
				"agent-stop:a",
			},
			"working",
			"1 agent running",
		},
		{"idle notification", []string{"prompt", "agent-start:a", "notification"}, "working", "1 agent running"},
		{
			"permission stays blocked",
			[]string{"prompt", "agent-start:a", "blocked", "agent-stop:a"},
			"blocked",
			"waiting for you",
		},
		// A sub-agent's dialog: its PermissionRequest and its call's own
		// PostToolUse carry agent_id, the dialog's Notification none.
		{
			"sub-agent permission blocks",
			[]string{"prompt", "agent-start:a", "idle", "agent-request:a/make", "permission-dialog"},
			"blocked",
			"waiting for approval: Bash",
		},
		{
			"sub-agent's approved call leaves its block",
			[]string{
				"prompt", "agent-start:a", "idle", "agent-request:a/make", "permission-dialog", "agent-ran:a/make",
			},
			"working",
			"1 agent running",
		},
		{
			"sub-agent's approved call that fails leaves its block",
			[]string{
				"prompt", "agent-start:a", "idle", "agent-request:a/make", "permission-dialog", "agent-failed:a/make",
			},
			"working",
			"1 agent running",
		},
		{
			"sub-agent's approved call during the main turn is working",
			[]string{"prompt", "agent-start:a", "agent-request:a/make", "permission-dialog", "agent-ran:a/make"},
			"working",
			"",
		},
		{
			"a sibling sub-agent's other tool leaves the block",
			[]string{"prompt", "agent-start:a", "agent-start:b", "idle", "agent-request:a/make", "agent-ran:b/ls"},
			"blocked",
			"waiting for approval: Bash",
		},
		{
			"a sibling sub-agent's identical call leaves the block",
			[]string{"prompt", "agent-start:a", "agent-start:b", "idle", "agent-request:a/make", "agent-ran:b/make"},
			"blocked",
			"waiting for approval: Bash",
		},
		{
			"a sub-agent's call never answers the main thread's request",
			[]string{"prompt", "agent-start:a", "main-request:make", "agent-ran:a/make"},
			"blocked",
			"waiting for approval: Bash",
		},
		{
			"a sub-agent's tool never leaves a block no tool asked for",
			[]string{"prompt", "agent-start:a", "idle", "permission-dialog", "agent-ran:a/ls"},
			"blocked",
			"Claude needs your permission",
		},
		{
			"a sibling sub-agent's stop leaves the open dialog blocked",
			[]string{"prompt", "agent-start:a", "agent-start:b", "idle", "agent-request:a/make", "agent-stop:b"},
			"blocked",
			"waiting for approval: Bash",
		},
		{
			"the main Stop leaves a sub-agent's open dialog blocked",
			[]string{"prompt", "agent-start:a", "agent-request:a/make", "permission-dialog", "idle", "notification"},
			"blocked",
			"waiting for approval: Bash",
		},
		{
			"the requesting sub-agent's stop after the main Stop is idle",
			[]string{"prompt", "agent-start:a", "agent-request:a/make", "permission-dialog", "idle", "agent-stop:a"},
			"idle",
			"",
		},
		{
			"an uncounted sub-agent's stop closes its dialog",
			[]string{"prompt", "idle", "agent-request:a/make", "permission-dialog", "agent-stop:a"},
			"idle",
			"",
		},
		{
			"the requesting sub-agent's stop closes its dialog",
			[]string{
				"prompt", "agent-start:a", "agent-start:b", "idle", "agent-request:a/make", "permission-dialog",
				"agent-stop:a",
			},
			"working",
			"1 agent running",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeHerdr(t)
			jail := newHerdrJail(t)
			now := int64(time.Second)
			for _, event := range test.events {
				activityTestEvent(t, jail, server, &now, event)
			}
			assertActivityReport(t, server, test.want, test.message)
			stored := jail.activityTmux.storedState(t)
			if stored.State != test.want || stored.Message != test.message {
				t.Fatalf("stored state=%s message=%q; want %s %q",
					stored.State, stored.Message, test.want, test.message)
			}
		})
	}
}

func activityTestEvent(t *testing.T, jail *herdrJail, server *fakeHerdr, now *int64, step string) {
	t.Helper()
	event, value, _ := strings.Cut(step, ":")
	transcript := filepath.Join(filepath.Dir(jail.lockDir), "activity-session.jsonl")
	if err := os.WriteFile(transcript, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"session_id": "activity-session", "transcript_path": transcript}
	agent, command, _ := strings.Cut(value, "/")
	toolCall := func(hook string) {
		payload["hook_event_name"] = hook
		payload["tool_name"] = "Bash"
		payload["tool_input"] = map[string]string{"command": command}
		if agent != "" {
			payload["agent_id"] = agent
		}
	}
	switch event {
	case "agent-request":
		event = "blocked"
		toolCall("PermissionRequest")
	case "main-request":
		event, agent, command = "blocked", "", value
		toolCall("PermissionRequest")
	case "agent-ran":
		event = "working"
		toolCall("PostToolUse")
	case "agent-failed":
		event = "working"
		toolCall("PostToolUseFailure")
	case "permission-dialog":
		event = "notification"
		payload["notification_type"] = "permission_prompt"
		payload["message"] = "Claude needs your permission"
	case "prompt":
		event = "working"
		payload["hook_event_name"] = "UserPromptSubmit"
	case "tool":
		event = "working"
		payload["hook_event_name"] = "PostToolUse"
	case "agent-start", "agent-stop":
		payload["agent_id"] = value
		payload["hook_event_name"] = "SubagentStart"
		if event == "agent-stop" {
			payload["hook_event_name"] = "SubagentStop"
		}
	case "start":
		payload["source"] = value
	case "notification":
		payload["notification_type"] = "idle_prompt"
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if jail.activityTmux == nil {
		jail.activityTmux = newFakeTmux("cc-1")
		jail.activityTmux.clients = []string{"4242"}
		jail.activityTmux.stored = mustJSON(
			herdrStored{
				Engine:    "claude",
				SessionID: "activity-session",
				Clients:   map[string]herdrClient{"4242": {Pane: "w1:p1", Socket: server.path, Terminal: "term_1"}},
			},
		)
		server.addPane("w1:p1", "term_1", 4242)
	}
	got := runHerdrIn(t, jail, activityEnv(server), jail.activityTmux, now, []string{event}, string(data))
	if got.rc != 0 || got.stderr != "" {
		t.Errorf("%s: rc=%d stderr=%s", step, got.rc, got.stderr)
	}
}

func assertActivityReport(t *testing.T, server *fakeHerdr, want, message string) {
	t.Helper()
	var state, text string
	for _, line := range server.requests() {
		var report struct {
			Method string `json:"method"`
			Params struct {
				State   string `json:"state"`
				Message string `json:"message"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &report); err != nil {
			t.Fatal(err)
		}
		if report.Method == "pane.report_agent" {
			state = report.Params.State
			text = report.Params.Message
		}
	}
	if state != want || text != message {
		t.Fatalf("Herdr state=%s message=%q; want %s %q", state, text, want, message)
	}
}

func TestHerdrLostAgentStopExpiresWithoutAnotherHook(t *testing.T) {
	server := newFakeHerdr(t)
	jail := newHerdrJail(t)
	now := int64(time.Second)
	var spawned [][]string
	jail.spawn = func(argv []string) error { spawned = append(spawned, argv); return nil }
	for _, step := range []string{"prompt", "agent-start:a", "idle"} {
		activityTestEvent(t, jail, server, &now, step)
	}
	if watchdogs := spawnedVerb(spawned, herdrAgentsWatch); len(watchdogs) != 1 {
		t.Errorf("detached watchdogs=%q; want 1", watchdogs)
	}
	now += int64(time.Hour + time.Second)
	var stderr bytes.Buffer
	code := routeHerdrState(
		append(
			[]string{"agents-watch"},
			activityWatchArgs(t, jail.deps(activityEnv(server), &now), "activity-session")...),
		strings.NewReader(""),
		&stderr,
		jail.deps(activityEnv(server), &now),
	)
	if code != 0 || stderr.Len() != 0 {
		t.Errorf("watch rc=%d stderr=%s", code, &stderr)
	}
	assertActivityReport(t, server, "idle", "")
}

// The watchdog's own re-send of idle, once the lease passed after a lost
// SubagentStop, can fail while Herdr does not answer. It clears the record's
// agents as it sends, so a retry of its own finds nothing left to expire: the
// undelivered re-send goes to the settle process, which delivers it once
// Herdr answers.
func TestHerdrAgentWatchdogUndeliveredResendIsSettled(t *testing.T) {
	server := newFakeHerdr(t)
	jail := newHerdrJail(t)
	now := int64(time.Second)
	var spawned [][]string
	jail.spawn = func(argv []string) error { spawned = append(spawned, argv); return nil }
	for _, step := range []string{"prompt", "agent-start:a", "idle"} {
		activityTestEvent(t, jail, server, &now, step)
	}
	deps := jail.deps(activityEnv(server), &now)
	args := activityWatchArgs(t, deps, "activity-session")
	now += int64(time.Hour + time.Second)
	away := server.path + ".away"
	if err := os.Rename(server.path, away); err != nil {
		t.Fatal(err)
	}
	waits := 0
	var stderr bytes.Buffer
	code := watchHerdrAgentsWithWait(args, &stderr, deps, func() {
		waits++
		if waits > 2 {
			t.Fatalf("watchdog never finished: %s", &stderr)
		}
		if err := os.Rename(away, server.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	})
	if code != 0 || !strings.Contains(stderr.String(), "retrying") {
		t.Fatalf("watchdog rc=%d stderr=%s, want exit 0 naming the undelivered re-send", code, &stderr)
	}
	runSpawned(t, slices.DeleteFunc(spawned, func(argv []string) bool { return argv[2] != herdrSettle }), deps)
	assertActivityReport(t, server, "idle", "")
}

func TestHerdrAgentPayloadRequiresIdentity(t *testing.T) {
	server := newFakeHerdr(t)
	jail := newHerdrJail(t)
	now := int64(time.Second)
	result := runHerdrIn(
		t,
		jail,
		herdrPaneEnv(server, "w1:p1"),
		nil,
		&now,
		[]string{"agent-start"},
		`{"session_id":"s1","hook_event_name":"SubagentStart"}`,
	)
	if result.rc != 0 || !strings.Contains(result.stderr, "agent_id") {
		t.Fatalf("missing agent id: %+v", result)
	}
	if len(server.requests()) != 0 {
		t.Fatal(fmt.Sprint("invalid payload reported: ", server.requests()))
	}
}

// A native Claude seat in a pane where Herdr lists no Claude in front reports
// its lifecycle under pfm's own source, with the same sub-agent hold as a tmux seat.
func TestHerdrNativeBackgroundAgents(t *testing.T) {
	root := t.TempDir()
	server := newNativeReporterHerdr(t, "native-seat", "w6:p3", "native-terminal")
	shellFrontNativeReporterPane(t, server, "w6:p3")
	t.Setenv(paths.EnvHome, root)
	t.Setenv(paths.EnvStateDB, filepath.Join(root, "pfm.db"))
	t.Setenv("TMUX", "")
	for key, value := range herdrPaneEnv(server, "w6:p3") {
		t.Setenv(key, value)
	}
	values := paths.Values{StateDB: filepath.Join(root, "pfm.db")}
	db := fleetdb.OpenSharedState(context.Background(), values)
	defer func() { _ = db.Close() }()
	seat := fleetdb.Seat{
		SeatID:      "native-seat",
		SessionID:   "activity-session",
		Backend:     "herdr",
		State:       "live",
		Engine:      "cc",
		HerdrSocket: server.path,
		HerdrPane:   "w6:p3",
	}
	stampNativeReporterSeat(t, &seat)
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(root, "activity-session.jsonl")
	if err := os.WriteFile(transcript, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deps := realHerdrDeps()
	now := time.Unix(1000, 0)
	deps.now = func() time.Time { now = now.Add(time.Millisecond); return now }
	var spawned [][]string
	deps.spawn = func(argv []string) error { spawned = append(spawned, argv); return nil }
	// The fake records each request's params with sorted keys, its seq left out.
	const argv = `"resume_argv":["pfm","chat","open","--restored","activity-session"]`
	report := func(state, message string) string {
		if message != "" {
			message = `"message":"` + message + `",`
		}
		return `pane.report_agent {"agent":"claude","agent_session_id":"activity-session",` + message +
			`"pane_id":"w6:p3",` + argv + `,"source":"pfm","state":"` + state + `"}`
	}
	working, idle := report("working", ""), report("idle", "")
	seen := 0
	lifecycle := func() []string {
		calls := server.calls()
		var got []string
		for _, call := range calls[seen:] {
			if strings.HasPrefix(call, "pane.report_agent") || strings.HasPrefix(call, "pane.release_agent") {
				got = append(got, call)
			}
		}
		seen = len(calls)
		return got
	}
	for _, test := range []struct {
		name, event, payload string
		want                 []string
	}{
		{
			"prompt reports working",
			"working",
			`{"session_id":"activity-session","hook_event_name":"UserPromptSubmit"}`,
			[]string{working},
		},
		{
			"agent start",
			"agent-start",
			`{"session_id":"activity-session","agent_id":"a","hook_event_name":"SubagentStart"}`,
			[]string{working},
		},
		{
			"Stop with a live agent stays working",
			"idle",
			`{"session_id":"activity-session"}`,
			[]string{report("working", "1 agent running")},
		},
		{
			"last agent stop after Stop reports idle",
			"agent-stop",
			`{"session_id":"activity-session","agent_id":"a","hook_event_name":"SubagentStop"}`,
			[]string{idle},
		},
		{
			"notification-started turn reads working on its first tool hook",
			"working",
			`{"session_id":"activity-session","hook_event_name":"PostToolUse","tool_name":"Read"}`,
			[]string{working},
		},
		{
			"permission request reports blocked",
			"blocked",
			`{"session_id":"activity-session","hook_event_name":"PermissionRequest","tool_name":"Bash"}`,
			[]string{report("blocked", "waiting for approval: Bash")},
		},
		{"Stop without agents reports idle", "idle", `{"session_id":"activity-session"}`, []string{idle}},
		{
			"late agent after Stop holds working",
			"agent-start",
			`{"session_id":"activity-session","agent_id":"b","hook_event_name":"SubagentStart"}`,
			[]string{report("working", "1 agent running")},
		},
	} {
		var stderr bytes.Buffer
		payload := strings.Replace(test.payload, `{"session_id":"activity-session"`,
			fmt.Sprintf(`{"session_id":"activity-session","transcript_path":%q`, transcript), 1)
		rc := reportToHerdr([]string{test.event}, strings.NewReader(payload), &stderr, deps)
		if rc != 0 || stderr.Len() != 0 {
			t.Fatalf("%s: rc=%d stderr=%s", test.name, rc, &stderr)
		}
		if got := lifecycle(); !slices.Equal(got, test.want) {
			t.Errorf(
				"%s: Herdr received\n%s\nwant\n%s",
				test.name,
				strings.Join(got, "\n"),
				strings.Join(test.want, "\n"),
			)
		}
	}
	// One watchdog per stopped turn holding an agent: the first Stop, the late
	// agent; one transcript watch per main turn: the prompt's, the resumed turn's.
	if watchdogs := spawnedVerb(spawned, herdrAgentsWatch); len(watchdogs) != 2 {
		t.Errorf("agent watchdogs=%q, want 2", watchdogs)
	}
	if watches := spawnedVerb(spawned, herdrWatchVerb); len(watches) != 2 {
		t.Errorf("transcript watches=%q, want 2", watches)
	}
	now = now.Add(time.Hour + time.Second)
	var stderr bytes.Buffer
	if rc := watchHerdrAgents(
		activityWatchArgs(t, deps, "activity-session"),
		&stderr,
		deps,
	); rc != 0 ||
		stderr.Len() != 0 {
		t.Fatalf("watch rc=%d stderr=%s", rc, &stderr)
	}
	if got := lifecycle(); !slices.Equal(got, []string{idle}) {
		t.Errorf("lost agent lease: Herdr received\n%s\nwant %s", strings.Join(got, "\n"), idle)
	}
}

func TestHerdrBackgroundAgentsThroughTmux(t *testing.T) {
	rig := newWatchRig(t)
	rig.wantQuiet(rig.engine("working", rig.payload(`,"hook_event_name":"UserPromptSubmit"`)))
	rig.wantQuiet(rig.engine("agent-start", `{"session_id":"s1","agent_id":"a","hook_event_name":"SubagentStart"}`))
	rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
	assertActivityReport(t, rig.herdr, "working", "1 agent running")
	rig.wantQuiet(rig.engine("agent-stop", `{"session_id":"s1","agent_id":"a","hook_event_name":"SubagentStop"}`))
	assertActivityReport(t, rig.herdr, "idle", "")
	// The hook's CLI event decides; a payload without its event name still counts.
	got := rig.engine("agent-start", `{"session_id":"s1","agent_id":"b"}`)
	want := `pfm internal herdr-state agent-start: hook_event_name "", want SubagentStart (counted as agent-start)` + "\n"
	if got.rc != 0 || got.stderr != want {
		t.Fatalf("rc=%d stderr=%q, want 0 %q", got.rc, got.stderr, want)
	}
	rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
	assertActivityReport(t, rig.herdr, "working", "1 agent running")
}

func TestHerdrAgentWatchDoesNotIdleANewPrompt(t *testing.T) {
	server := newFakeHerdr(t)
	jail := newHerdrJail(t)
	now := int64(time.Second)
	for _, event := range []string{"prompt", "agent-start:a", "idle", "prompt"} {
		activityTestEvent(t, jail, server, &now, event)
	}
	now += int64(time.Hour + time.Second)
	var stderr bytes.Buffer
	if rc := watchHerdrAgents(
		activityWatchArgs(t, jail.deps(activityEnv(server), &now), "activity-session"),
		&stderr,
		jail.deps(activityEnv(server), &now),
	); rc != 0 ||
		stderr.Len() != 0 {
		t.Fatalf("watch rc=%d stderr=%s", rc, &stderr)
	}
	assertActivityReport(t, server, "working", "")
}

func TestHerdrInterruptWithBackgroundAgentStaysWorking(t *testing.T) {
	rig := newWatchRig(t)
	rig.wantQuiet(rig.engine("agent-start", `{"session_id":"s1","agent_id":"a","hook_event_name":"SubagentStart"}`))
	argv := rig.block("Bash")
	rig.appendEntries(watchRejectionEntry, watchInterruptEntry)
	watch := rig.start(argv, fastPoll)
	watch.wait(t, time.Second)
	assertActivityReport(t, rig.herdr, "working", "1 agent running")
	if state := rig.tmux.storedState(t); state.State != "working" || len(state.Agents) != 1 {
		t.Fatalf("stored interrupted state=%+v", state)
	}
}

func TestHerdrAgentLeaseIsNotExtendedByIdleOrAgentEvents(t *testing.T) {
	server := newFakeHerdr(t)
	jail := newHerdrJail(t)
	now := int64(time.Second)
	for _, event := range []string{"prompt", "agent-start:a", "idle"} {
		activityTestEvent(t, jail, server, &now, event)
	}
	reporter := &herdrReporter{deps: jail.deps(activityEnv(server), &now)}
	before, err := reporter.readActivity("activity-session")
	if err != nil {
		t.Fatal(err)
	}
	now += int64(time.Minute * 59)
	for _, event := range []string{"notification", "agent-start:b"} {
		activityTestEvent(t, jail, server, &now, event)
	}
	after, err := reporter.readActivity("activity-session")
	if err != nil {
		t.Fatal(err)
	}
	if before.Stored.MainStoppedAt != after.Stored.MainStoppedAt {
		t.Fatal("idle/tool/agent hook extended the lease")
	}
	done, err := reporter.expireAgents("activity-session")
	if err != nil || done {
		t.Fatalf("expired before bound: done=%v err=%v", done, err)
	}
	now += int64(time.Minute + time.Second)
	done, err = reporter.expireAgents("activity-session")
	if err != nil || !done {
		t.Fatalf("not expired at bound: done=%v err=%v", done, err)
	}
	assertActivityReport(t, server, "idle", "")
}

func TestHerdrAgentWatchNeverIdlesAReplacementOwner(t *testing.T) {
	server := newFakeHerdr(t)
	jail := newHerdrJail(t)
	now := int64(time.Second)
	for _, event := range []string{"prompt", "agent-start:a", "idle"} {
		activityTestEvent(t, jail, server, &now, event)
	}
	reporter := &herdrReporter{
		deps:    jail.deps(activityEnv(server), &now),
		session: "replacement",
		seq:     uint64(now + 1),
	}
	if err := reporter.reservePane(
		herdrTarget{socket: server.path, pane: "w1:p1", terminal: "term_1", client: "different-client"},
	); err != nil {
		t.Fatal(err)
	}
	now += int64(time.Hour + time.Second)
	done, err := reporter.expireAgents("activity-session")
	if err != nil || !done {
		t.Fatalf("watch done=%v err=%v", done, err)
	}
	assertActivityReport(t, server, "working", "1 agent running")
}

func activityWatchArgs(t *testing.T, deps herdrDeps, session string) []string {
	t.Helper()
	reporter := &herdrReporter{deps: deps}
	state, err := reporter.readActivity(session)
	if err != nil {
		t.Fatal(err)
	}
	return []string{session, strconv.FormatInt(state.Stored.MainStoppedAt, 10)}
}

func TestHerdrLateAgentStartGetsItsOwnWatch(t *testing.T) {
	server := newFakeHerdr(t)
	jail := newHerdrJail(t)
	now := int64(time.Second)
	var spawned [][]string
	jail.spawn = func(argv []string) error { spawned = append(spawned, argv); return nil }
	for _, event := range []string{"prompt", "idle", "agent-start:a"} {
		activityTestEvent(t, jail, server, &now, event)
	}
	if watchdogs := spawnedVerb(spawned, herdrAgentsWatch); len(watchdogs) != 1 {
		t.Fatalf("late start watchdogs=%q; want 1", watchdogs)
	}
}

func TestHerdrTmuxAgentLeaseExpiresStoredState(t *testing.T) {
	rig := newWatchRig(t)
	rig.wantQuiet(rig.engine("agent-start", `{"session_id":"s1","agent_id":"a","hook_event_name":"SubagentStart"}`))
	rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
	now := rig.clock + int64(time.Hour+time.Second)
	deps := rig.jail.deps(rig.env, &now)
	deps.tmux = rig.tmux.run
	deps.listPanes = rig.tmux.listPanes
	reporter := &herdrReporter{deps: deps}
	done, err := reporter.expireAgents("s1")
	if err != nil || !done {
		t.Fatalf("expire done=%v err=%v", done, err)
	}
	stored := rig.tmux.storedState(t)
	if stored.State != "idle" || len(stored.Agents) != 0 {
		t.Fatalf("stored expired state=%+v", stored)
	}
	assertActivityReport(t, rig.herdr, "idle", "")
}

func TestHerdrActivityRejectsUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			jail := newHerdrJail(t)
			now := int64(time.Second)
			reporter := &herdrReporter{deps: jail.deps(map[string]string{}, &now)}
			path := reporter.activityPath("s1")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "symlink":
				target := filepath.Join(t.TempDir(), "target")
				if err = os.WriteFile(target, []byte(`{}`), 0o600); err == nil {
					err = os.Symlink(target, path)
				}
			case "fifo":
				err = syscall.Mkfifo(path, 0o600)
			case "oversize":
				err = os.WriteFile(path, make([]byte, (1<<20)+1), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reporter.readActivity("s1"); err == nil {
				t.Fatal("unsafe activity file accepted")
			}
		})
	}
}

func TestHerdrActivityEndRemovesRecord(t *testing.T) {
	for _, reason := range []string{"logout", "clear", "resume"} {
		t.Run(reason, func(t *testing.T) {
			server := newFakeHerdr(t)
			jail := newHerdrJail(t)
			now := int64(time.Second)
			activityTestEvent(t, jail, server, &now, "prompt")
			got := runHerdrIn(t, jail, activityEnv(server), jail.activityTmux, &now, []string{"end"},
				fmt.Sprintf(`{"session_id":"activity-session","reason":%q}`, reason))
			if got.rc != 0 || got.stderr != "" {
				t.Fatalf("end: %+v", got)
			}
			reporter := &herdrReporter{deps: jail.deps(activityEnv(server), &now)}
			if _, err := os.Stat(reporter.activityPath("activity-session")); !os.IsNotExist(err) {
				t.Fatalf("ended record retained: %v", err)
			}
		})
	}
}

func TestHerdrAgentWatchRetriesAReadFailure(t *testing.T) {
	server := newFakeHerdr(t)
	jail := newHerdrJail(t)
	now := int64(time.Second)
	for _, step := range []string{"prompt", "agent-start:a", "idle"} {
		activityTestEvent(t, jail, server, &now, step)
	}
	deps := jail.deps(activityEnv(server), &now)
	reporter := &herdrReporter{deps: deps}
	args := activityWatchArgs(t, deps, "activity-session")
	path := reporter.activityPath("activity-session")
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// An unreadable record (here: not a regular file) is retried; an undecodable
	// one is a corrupt state the next hook rewrites.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	waits := 0
	var stderr bytes.Buffer
	code := watchHerdrAgentsWithWait(args, &stderr, deps, func() {
		waits++
		if waits > 1 {
			t.Fatal("watch did not recover")
		}
		now += int64(time.Hour + time.Second)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, good, 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if code != 0 || waits != 1 || !strings.Contains(stderr.String(), "retrying") {
		t.Fatalf("retry: code=%d waits=%d stderr=%s", code, waits, &stderr)
	}
	assertActivityReport(t, server, "idle", "")
}

// The watch's idle never lowers the activity record's stale-event fence: a
// held hook (a parallel tool finishing while the dialog stays open) advanced
// the record above the tmux block, and the interrupt after it is stored at the
// interrupt's own later time.
func TestHerdrTranscriptWatchPreservesActivityFence(t *testing.T) {
	rig := newWatchRig(t)
	argv := rig.block("Bash")
	blocked := rig.tmux.storedState(t)
	rig.wantQuiet(rig.engine("working", rig.payload(`,"hook_event_name":"PostToolUse","tool_name":"Read"`)))
	reporter := &herdrReporter{deps: rig.watchDeps()}
	before, err := reporter.readActivity("s1")
	if err != nil {
		t.Fatal(err)
	}
	if before.Stored.Seq <= blocked.Seq || rig.tmux.storedState(t).Seq != blocked.Seq {
		t.Fatal("fixture did not advance activity independently of tmux")
	}
	watch := rig.start(argv, fastPoll)
	rig.appendEntries(watchRejectionEntry, watchInterruptEntry)
	watch.wait(t, 3*time.Second)
	after, err := reporter.readActivity("s1")
	if err != nil || after.Stored.State != "idle" || after.Stored.Seq != watchInterruptAt {
		t.Fatalf("activity after the interrupt = %+v (err %v), want idle at seq %d above the held %d",
			after.Stored, err, uint64(watchInterruptAt), before.Stored.Seq)
	}
}

// A tmux session that holds another conversation by the time the interrupt
// lands keeps that conversation's state: the watch settles only its own.
func TestHerdrTranscriptWatchDoesNotCrossSessions(t *testing.T) {
	rig := newWatchRig(t)
	argv := rig.block("Bash")
	replacement := rig.tmux.storedState(t)
	replacement.SessionID = "replacement-session"
	rig.seed(replacement)
	before, sent := rig.tmux.stored, len(rig.herdr.calls())
	watch := rig.start(argv, fastPoll)
	rig.appendEntries(watchRejectionEntry, watchInterruptEntry)
	watch.wait(t, 3*time.Second)
	if got := rig.herdr.calls()[sent:]; len(got) != 0 || rig.tmux.stored != before {
		t.Fatalf("requests %q, stored %q, want none and the replacement's %q", got, rig.tmux.stored, before)
	}
	reporter := &herdrReporter{deps: rig.watchDeps()}
	if _, err := os.Stat(reporter.activityPath("replacement-session")); !os.IsNotExist(err) {
		t.Fatalf("watch touched a session it does not watch: %v", err)
	}
	if own, err := reporter.readActivity("s1"); err != nil || own.Stored.State != "idle" {
		t.Fatalf("own activity = %+v (err %v), want the interrupted s1 idle", own.Stored, err)
	}
}

// A native Claude seat interrupted mid-turn settles through the settle
// process a Stop hook falls back to (a detached watch cannot prove the seat
// by its own ancestry): idle, or working "1 agent running" while a sub-agent
// remains, and the watch exits with its record gone.
func TestHerdrNativeWatchReportsIdleOnTheInterruptEntry(t *testing.T) {
	for _, test := range []struct {
		name, state, message string
		before               []string
	}{
		{"Esc while the model streams", "idle", "", []string{"prompt"}},
		{"Esc with a sub-agent running", "working", "1 agent running", []string{"prompt", "agent-start"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rig := newNativeActivityRig(t)
			for _, step := range test.before {
				if got := rig.event(step); got.rc != 0 || got.stderr != "" {
					t.Fatalf("%s: rc=%d stderr=%s", step, got.rc, got.stderr)
				}
			}
			watches := spawnedVerb(rig.spawned, herdrWatchVerb)
			if len(watches) != 1 {
				t.Fatalf("spawned %q, want one transcript watch", rig.spawned)
			}
			argv := watches[0]
			// 900002 is the fixture's claude, between the seat host and the hook.
			if slices.Contains(argv, herdrSocketFlag) || argv[len(argv)-1] != "activity-session" ||
				flagValue(t, argv, "--pid") != "900002" || flagValue(t, argv, "--command") != "claude" {
				t.Fatalf("watch argv = %q, want no tmux socket, session activity-session, engine 900002 claude", argv)
			}
			settles := make(chan []string, 1)
			deps := rig.deps
			deps.spawn = func(argv []string) error {
				if argv[2] == herdrSettle {
					settles <- argv
				}
				return nil
			}
			var stderr bytes.Buffer
			done := make(chan int)
			go func() { done <- watchForHerdr(argv[3:], &stderr, deps, fastPoll) }()
			file, err := os.OpenFile(rig.transcript, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteString(watchModelInterruptEntry + "\n"); err != nil {
				t.Fatal(err)
			}
			_ = file.Close()
			var settle []string
			select {
			case settle = <-settles:
			case <-time.After(3 * time.Second):
				t.Fatal("the watch started no settle process within 3s of the interrupt entry")
			}
			runSpawned(t, [][]string{settle}, rig.deps)
			select {
			case rc := <-done:
				if rc != 0 || stderr.Len() != 0 {
					t.Fatalf("watch rc=%d stderr=%s, want a quiet exit 0", rc, &stderr)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("the watch still runs 3s after its idle was recorded")
			}
			assertActivityReport(t, rig.server, test.state, test.message)
			reporter := &herdrReporter{deps: rig.deps}
			if _, err := os.Stat(reporter.watchRecord("activity-session")); !os.IsNotExist(err) {
				t.Fatalf("watch record after the idle: %v, want it gone", err)
			}
		})
	}
}

// spawnedVerb is every spawned argv of one herdr-state verb.
func spawnedVerb(spawned [][]string, verb string) [][]string {
	var argvs [][]string
	for _, argv := range spawned {
		if len(argv) > 2 && argv[2] == verb {
			argvs = append(argvs, argv)
		}
	}
	return argvs
}

func activityEnv(server *fakeHerdr) map[string]string {
	env := herdrPaneEnv(server, "w1:p1")
	env["TMUX"], env["TMUX_PANE"] = rigSocket+",1,0", "%0"
	return env
}
