package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

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
		})
	}
}

func activityTestEvent(t *testing.T, jail *herdrJail, server *fakeHerdr, now *int64, step string) {
	t.Helper()
	event, value, _ := strings.Cut(step, ":")
	payload := map[string]string{"session_id": "activity-session"}
	switch event {
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
	got := runHerdrIn(t, jail, herdrPaneEnv(server, "w1:p1"), nil, now, []string{event}, string(data))
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
	if len(spawned) != 1 {
		t.Errorf("detached watchdogs=%d; want 1", len(spawned))
	}
	now += int64(time.Hour + time.Second)
	var stderr bytes.Buffer
	code := routeHerdrState(
		append(
			[]string{"agents-watch"},
			activityWatchArgs(t, jail.deps(herdrPaneEnv(server, "w1:p1"), &now), "activity-session")...),
		strings.NewReader(""),
		&stderr,
		jail.deps(herdrPaneEnv(server, "w1:p1"), &now),
	)
	if code != 0 || stderr.Len() != 0 {
		t.Errorf("watch rc=%d stderr=%s", code, &stderr)
	}
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

func TestHerdrNativeBackgroundAgents(t *testing.T) {
	root := t.TempDir()
	server := newNativeReporterHerdr(t, "native-seat", "w6:p3", "native-terminal")
	server.addPane("w6:p3", "native-terminal")
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
	deps := realHerdrDeps()
	now := time.Unix(1000, 0)
	deps.now = func() time.Time { now = now.Add(time.Millisecond); return now }
	deps.spawn = func([]string) error { return nil }
	for _, test := range []struct{ event, payload, want, message string }{
		{"working", `{"session_id":"activity-session","hook_event_name":"UserPromptSubmit"}`, "working", ""},
		{"agent-start", `{"session_id":"activity-session","agent_id":"a","hook_event_name":"SubagentStart"}`, "working", ""},
		{"idle", `{"session_id":"activity-session"}`, "working", "1 agent running"},
		{"agent-stop", `{"session_id":"activity-session","agent_id":"a","hook_event_name":"SubagentStop"}`, "idle", ""},
		{"agent-start", `{"session_id":"activity-session","agent_id":"b","hook_event_name":"SubagentStart"}`, "working", "1 agent running"},
	} {
		var stderr bytes.Buffer
		rc := reportToHerdr([]string{test.event}, strings.NewReader(test.payload), &stderr, deps)
		if rc != 0 || stderr.Len() != 0 {
			t.Fatalf("%s: rc=%d stderr=%s", test.event, rc, &stderr)
		}
		assertActivityReport(t, server, test.want, test.message)
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
	assertActivityReport(t, server, "idle", "")
}

func TestHerdrBackgroundAgentsThroughTmux(t *testing.T) {
	rig := newWatchRig(t)
	rig.wantQuiet(rig.engine("working", `{"session_id":"s1","hook_event_name":"UserPromptSubmit"}`))
	rig.wantQuiet(rig.engine("agent-start", `{"session_id":"s1","agent_id":"a","hook_event_name":"SubagentStart"}`))
	rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
	assertActivityReport(t, rig.herdr, "working", "1 agent running")
	rig.wantQuiet(rig.engine("agent-stop", `{"session_id":"s1","agent_id":"a","hook_event_name":"SubagentStop"}`))
	assertActivityReport(t, rig.herdr, "idle", "")
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
		activityWatchArgs(t, jail.deps(herdrPaneEnv(server, "w1:p1"), &now), "activity-session"),
		&stderr,
		jail.deps(herdrPaneEnv(server, "w1:p1"), &now),
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
	watch := rig.start(argv, fastWatch)
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
	reporter := &herdrReporter{deps: jail.deps(herdrPaneEnv(server, "w1:p1"), &now)}
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
		deps:    jail.deps(herdrPaneEnv(server, "w1:p1"), &now),
		session: "replacement",
		seq:     uint64(now + 1),
	}
	if err := reporter.reservePane(
		herdrTarget{socket: server.path, pane: "w1:p1", client: "different-client"},
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
	spawned := 0
	jail.spawn = func([]string) error { spawned++; return nil }
	for _, event := range []string{"prompt", "idle", "agent-start:a"} {
		activityTestEvent(t, jail, server, &now, event)
	}
	if spawned != 1 {
		t.Fatalf("late start watchdogs=%d; want 1", spawned)
	}
}

func TestHerdrTmuxAgentLeaseExpiresStoredState(t *testing.T) {
	rig := newWatchRig(t)
	rig.wantQuiet(rig.engine("agent-start", `{"session_id":"s1","agent_id":"a","hook_event_name":"SubagentStart"}`))
	rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
	now := rig.clock + int64(time.Hour+time.Second)
	deps := rig.jail.deps(rig.env, &now)
	deps.tmux = rig.tmux.run
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
			got := runHerdrIn(t, jail, herdrPaneEnv(server, "w1:p1"), nil, &now, []string{"end"},
				fmt.Sprintf(`{"session_id":"activity-session","reason":%q}`, reason))
			if got.rc != 0 || got.stderr != "" {
				t.Fatalf("end: %+v", got)
			}
			reporter := &herdrReporter{deps: jail.deps(herdrPaneEnv(server, "w1:p1"), &now)}
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
	deps := jail.deps(herdrPaneEnv(server, "w1:p1"), &now)
	reporter := &herdrReporter{deps: deps}
	args := activityWatchArgs(t, deps, "activity-session")
	path := reporter.activityPath("activity-session")
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{`), 0o600); err != nil {
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
		if err := os.WriteFile(path, good, 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if code != 0 || waits != 1 || !strings.Contains(stderr.String(), "retrying") {
		t.Fatalf("retry: code=%d waits=%d stderr=%s", code, waits, &stderr)
	}
	assertActivityReport(t, server, "idle", "")
}

func TestHerdrTranscriptWatchPreservesActivityFence(t *testing.T) {
	rig := newWatchRig(t)
	rig.block("Bash")
	blocked := rig.tmux.storedState(t)
	rig.wantQuiet(rig.engine("working", `{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Read"}`))
	deps := rig.jail.deps(rig.env, &rig.clock)
	deps.tmux = rig.tmux.run
	options, err := parseHerdrOptions([]string{"idle", "--socket", rigSocket, "--session", "cc-1"})
	if err != nil {
		t.Fatal(err)
	}
	reporter := &herdrReporter{deps: deps, options: options, session: "cc-1"}
	before, err := reporter.readActivity("s1")
	if err != nil {
		t.Fatal(err)
	}
	if before.Stored.Seq <= blocked.Seq {
		t.Fatal("fixture did not advance activity independently of tmux")
	}
	watch := &herdrWatch{reporter: reporter, seq: blocked.Seq}
	if err := watch.report(); err != nil {
		t.Fatal(err)
	}
	after, err := reporter.readActivity("s1")
	if err != nil || after.Stored.Seq <= before.Stored.Seq {
		t.Fatalf(
			"activity sequence moved backward: before=%d after=%d err=%v",
			before.Stored.Seq,
			after.Stored.Seq,
			err,
		)
	}
}

func TestHerdrTranscriptWatchDoesNotCrossSessions(t *testing.T) {
	rig := newWatchRig(t)
	rig.block("Bash")
	blocked := rig.tmux.storedState(t)
	deps := rig.jail.deps(rig.env, &rig.clock)
	reads := 0
	deps.tmux = func(ctx context.Context, socket string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "show-options" && args[len(args)-1] == herdrStoredOption {
			reads++
			if reads == 2 {
				changed := blocked
				changed.SessionID = "replacement-session"
				rig.seed(changed)
			}
		}
		return rig.tmux.run(ctx, socket, args...)
	}
	options, err := parseHerdrOptions([]string{"idle", "--socket", rigSocket, "--session", "cc-1"})
	if err != nil {
		t.Fatal(err)
	}
	reporter := &herdrReporter{deps: deps, options: options, session: "cc-1"}
	watch := &herdrWatch{reporter: reporter, seq: blocked.Seq}
	if err := watch.report(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(reporter.activityPath("replacement-session")); !os.IsNotExist(err) {
		t.Fatalf("watch touched a session whose lock it never held: %v", err)
	}
}
