package restart

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	herdrpkg "github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// fakeServices is a ServiceManager over fixed units, recording what it was asked.
type fakeServices struct {
	units []Unit
	err   error
}

func (services *fakeServices) Units(
	_ context.Context,
	family string,
	match func(string) bool,
	verify Verifier,
) ([]Unit, string, error) {
	var units []Unit
	for index := range services.units {
		unit := services.units[index]
		name := strings.TrimPrefix(strings.TrimPrefix(unit.Name, "launchd "), "systemd ")
		if match(name) {
			unit.Family = family
			if probe := verify.check(name); probe != nil {
				unit.StartHow += " + probe"
			}
			units = append(units, unit)
		}
	}
	return units, "fake manager", services.err
}

func serviceUnit(order *[]string, name string) Unit {
	return Unit{
		Name: name, FoundBy: "fake", StopHow: "stop", StartHow: "start",
		Stop:  func(context.Context) (string, error) { *order = append(*order, "stop "+name); return "stopped", nil },
		Start: func(context.Context) (string, error) { *order = append(*order, "start "+name); return "up", nil },
	}
}

// fullHost is one host with something in every family; order records every step the plan runs.
func fullHost(t *testing.T, order *[]string) (Deps, *hostRunner, *fakeKernel, *fakeFleet) {
	t.Helper()
	herdr := &herdrModel{running: true}
	runner := &hostRunner{
		paths: map[string]string{"herdr": "/opt/bin/herdr"},
		handle: func(argv []string) (deps.RunResult, error) {
			if strings.HasSuffix(argv[0], "herdr") {
				return herdr.run(argv)
			}
			return deps.RunResult{}, fmt.Errorf("unexpected command %v", argv)
		},
		onStart: herdr.started,
	}
	kernel := newKernel(
		process{PID: 10, Argv: []string{"pfm", "internal", "cost-sink"}},
		process{PID: 11, Argv: []string{"pfm", "mcp", "serve"}},
		process{PID: 12, Argv: []string{"pfm", "ls", "--watch"}},
		process{PID: 13, Argv: []string{"pfm", "restart"}},
		process{PID: herdrServerPID, Argv: []string{"/opt/bin/herdr", "server"}},
	)
	herdr.onStop = func() {
		kernel.mu.Lock()
		defer kernel.mu.Unlock()
		delete(kernel.procs, herdrServerPID)
	}
	fleet := &fakeFleet{chats: []headless.Chat{
		{Engine: pfmengine.Claude, Name: "launcher", ID: "sess-launch", Socket: "pfm-cc-launcher", Pane: "%0"},
		{Engine: pfmengine.Codex, Name: "builder", ID: "sess-build", Socket: "pfm-cx-builder", Pane: "%1"},
		{
			Engine: pfmengine.OpenCode, Name: "", ID: "sess-oc", Backend: spawn.BackendHerdr, SeatID: "seat-3",
			Socket: "/run/herdr.sock", Pane: "w1:p3",
		},
	}}
	// The Herdr chat is back only once the restart started the Herdr server again.
	fleet.back = func(chat headless.Chat, _ string) (bool, string, error) {
		*order = append(*order, "back "+chat.ID)
		for index := range runner.starts {
			if runner.starts[index].Argv[0] == "/opt/bin/herdr" {
				return true, "seat " + chat.SeatID + " live in Herdr pane " + chat.Pane, nil
			}
		}
		return false, "the Herdr server is not started", nil
	}
	dependencies := testDeps(t, runner, kernel, fleet)
	dependencies.Spare = []int{13}
	dependencies.LaunchedFrom = chatAddress{Socket: "pfm-cc-launcher"}
	dependencies.DaemonProbe = func(context.Context) error { return nil }
	dependencies.Services = &fakeServices{units: []Unit{serviceUnit(order, "systemd pfm-schedule.timer")}}
	return dependencies, runner, kernel, fleet
}

func TestBuildPlanOrdersEveryFamilyDownstreamFirst(t *testing.T) {
	var order []string
	dependencies, _, _, _ := fullHost(t, &order)
	plan := BuildPlan(context.Background(), dependencies)
	for _, finding := range plan.Findings {
		if finding.Err != nil {
			t.Fatalf("finding failed: %s: %v", finding.Family, finding.Err)
		}
	}
	want := []string{
		"chats: codex builder session sess-build",
		"chats: opencode (unnamed) session sess-oc",
		"chats: claude launcher session sess-launch",
		"chat-helpers: cost sink pid 10",
		"herdr: herdr server /run/herdr.sock",
		"services: systemd pfm-schedule.timer",
		"mcp-daemon: pfm mcp serve pid 11",
		"pfm: pfm process pid 12",
	}
	if got := unitNames(plan.Units); !slices.Equal(got, want) {
		t.Fatalf("plan =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(plan.Units[2].FoundBy, "(the chat that typed pfm restart)") {
		t.Fatalf("the launching chat is not marked: %q", plan.Units[2].FoundBy)
	}
	if !strings.Contains(plan.Units[1].FoundBy, "Herdr seat seat-3") {
		t.Fatalf("a Herdr chat is not found by its seat: %q", plan.Units[1].FoundBy)
	}
	if !strings.Contains(plan.Last, "pid 1") {
		t.Fatalf("last = %q, want this worker named", plan.Last)
	}
}

// TestDryRunChangesNothing: --dry-run prints the plan a real run would carry
// out, today's every-chat-ended plan under --no-restore, and runs nothing but
// the reads, each once.
func TestDryRunChangesNothing(t *testing.T) {
	for _, row := range []struct {
		golden    string
		noRestore bool
	}{
		{golden: "dry_run_plan.txt", noRestore: true},
		{golden: "dry_run_plan_restore.txt", noRestore: false},
	} {
		t.Run(row.golden, func(t *testing.T) {
			var order []string
			dependencies, runner, kernel, fleet := fullHost(t, &order)
			dependencies.NoRestore = row.noRestore
			plan := BuildPlan(context.Background(), dependencies)
			var out bytes.Buffer
			plan.Print(&out)
			if len(order) != 0 || len(kernel.sent()) != 0 || len(runner.starts) != 0 || len(fleet.ended) != 0 ||
				len(fleet.polled) != 0 {
				t.Fatalf("the dry run changed something: steps %v, signals %v, starts %v, ended %v, polled %v",
					order, kernel.sent(), runner.starts, fleet.ended, fleet.polled)
			}
			ran := make([]string, 0, len(runner.ran()))
			for _, argv := range runner.ran() {
				ran = append(ran, strings.Join(argv, " "))
			}
			golden(t, row.golden, out.String()+"--- ran\n"+strings.Join(ran, "\n")+"\n")
		})
	}
}

func TestDryRunNamesTheWorkerARealRunStarts(t *testing.T) {
	var order []string
	dependencies, _, _, _ := fullHost(t, &order)
	dependencies.DryRun = true
	plan := BuildPlan(context.Background(), dependencies)
	if plan.Last != "the restart worker a plain pfm restart detaches (pfm internal restart-run) — exits after the start phase, the last pfm process" {
		t.Fatalf("dry-run last = %q, want the worker a real run would start, not this process", plan.Last)
	}
	for _, finding := range plan.Findings {
		if finding.Family == FamilyPFM &&
			finding.Detail != "1 other pfm process(es) of 5 listed — process table, argv[0] named pfm; the restart worker is last" {
			t.Fatalf("dry-run pfm finding = %q", finding.Detail)
		}
	}
}

// TestAHerdrLaunchingChatEndsLastOfTheChats: typed in a chat that runs
// directly in a Herdr pane (no TMUX), the restart still ends that chat last,
// found by the --self identity's Herdr socket and pane.
func TestAHerdrLaunchingChatEndsLastOfTheChats(t *testing.T) {
	original := restartIdentify
	t.Cleanup(func() { restartIdentify = original })
	restartIdentify = func(context.Context) (resolve.Identity, error) {
		return resolve.Identity{Backend: "herdr", SocketPath: "/run/herdr.sock", Pane: "w1:p2"}, nil
	}

	t.Setenv("TMUX", "")
	t.Setenv(herdrpkg.EnvMarker, "1")
	t.Setenv(herdrpkg.EnvSocket, "/run/herdr.sock")
	t.Setenv(herdrpkg.EnvPane, "w1:p2")
	fleet := &fakeFleet{chats: []headless.Chat{
		{
			Engine: pfmengine.Claude, Name: "launcher", ID: "sess-launch", Backend: spawn.BackendHerdr,
			SeatID: "seat-1", Socket: "/run/herdr.sock", Pane: "w1:p2",
		},
		{
			Engine: pfmengine.Codex, Name: "neighbour", ID: "sess-near", Backend: spawn.BackendHerdr,
			SeatID: "seat-2", Socket: "/run/herdr.sock", Pane: "w1:p3",
		},
		{Engine: pfmengine.Codex, Name: "builder", ID: "sess-build", Socket: "pfm-cx-builder", Pane: "%1"},
	}}
	dependencies := testDeps(t, &hostRunner{}, newKernel(), fleet)
	var identifyErr error
	dependencies.LaunchedFrom, identifyErr = launchingChat()
	if identifyErr != nil {
		t.Fatal(identifyErr)
	}
	plan := BuildPlan(context.Background(), dependencies)
	var chats []string
	for index := range plan.Units {
		unit := &plan.Units[index]
		if unit.Family == FamilyChats {
			chats = append(chats, unit.Chat.SessionID)
		}
	}
	if !slices.Equal(chats, []string{"sess-near", "sess-build", "sess-launch"}) {
		t.Fatalf("chats in stop order = %v, want the Herdr chat that typed the restart last", chats)
	}
	if !strings.Contains(plan.Units[2].FoundBy, "(the chat that typed pfm restart)") {
		t.Fatalf("the launching Herdr chat is not marked: %q", plan.Units[2].FoundBy)
	}
}

// TestExecuteRunsTheWholeRestartAndEndsTheLaunchingChatLastOfTheChats: a
// plain restart leaves the Herdr chat for the Herdr server stop and verifies
// it back after the server starts, ending the tmux chats with why; under
// --no-restore it ends every chat, the launching one last.
func TestExecuteRunsTheWholeRestartAndEndsTheLaunchingChatLastOfTheChats(t *testing.T) {
	for _, row := range []struct {
		name      string
		noRestore bool
		ended     []string
		order     []string
		chats     string
	}{
		{
			name: "no restore", noRestore: true,
			ended: []string{"sess-build", "sess-oc", "sess-launch"},
			order: []string{"stop systemd pfm-schedule.timer", "start systemd pfm-schedule.timer"},
			chats: `restart: ended 3 chat(s)
  ended  codex    builder                  sess-build
  ended  opencode (unnamed)                sess-oc
  ended  claude   launcher                 sess-launch
`,
		},
		{
			name:  "restore",
			ended: []string{"sess-build", "sess-launch"},
			order: []string{"stop systemd pfm-schedule.timer", "start systemd pfm-schedule.timer", "back sess-oc"},
			chats: `restart: ended 2 chat(s)
  ended  codex    builder                  sess-build  not brought back: it runs in tmux, and only a chat in a Herdr pane comes back with Herdr's restore
  ended  claude   launcher                 sess-launch  not brought back: it runs in tmux, and only a chat in a Herdr pane comes back with Herdr's restore
restart: brought back 1 chat(s)
  back   opencode (unnamed)                sess-oc
`,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			var order []string
			dependencies, runner, kernel, fleet := fullHost(t, &order)
			dependencies.NoRestore = row.noRestore
			plan := BuildPlan(context.Background(), dependencies)
			var out bytes.Buffer
			if code := plan.Execute(context.Background(), &out); code != 0 {
				t.Fatalf("Execute = %d:\n%s", code, out.String())
			}
			if !slices.Equal(fleet.ended, row.ended) {
				t.Fatalf("ended = %v, want %v, the launching chat last", fleet.ended, row.ended)
			}
			if !slices.Equal(order, row.order) {
				t.Fatalf("steps = %v, want %v", order, row.order)
			}
			if got := kernel.sent(); !slices.Equal(got, []string{"10 terminated", "11 terminated", "12 terminated"}) {
				t.Fatalf("signals = %v (pid 13, the front, must be spared)", got)
			}
			var started []string
			for _, call := range runner.starts {
				started = append(started, strings.Join(call.Argv, " "))
			}
			if !slices.Equal(started, []string{"/opt/pfm/bin/pfm mcp serve", "/opt/bin/herdr server"}) {
				t.Fatalf("starts = %v, want the daemon before Herdr", started)
			}
			text := out.String()
			from, to := strings.Index(text, "restart: ended"), strings.Index(text, "last   pfm")
			if from < 0 || to < from || text[from:to] != row.chats {
				t.Fatalf(
					"chats report:\n%s\nwant:\n%s\nwhole output:\n%s",
					text[max(from, 0):max(to, 0)],
					row.chats,
					text,
				)
			}
			if !strings.HasSuffix(text, "restart: every step verified\nrestart: exit 0\n") {
				t.Fatalf("output does not end verified:\n%s", text)
			}
		})
	}
}

func TestFindChatsAFailedScanAndAnUnreadableSocketAreNamed(t *testing.T) {
	finder := &discoverer{
		deps: testDeps(t, &hostRunner{}, newKernel(), &fakeFleet{scanErr: errors.New("tmux missing")}),
	}
	finder.findChats(context.Background())
	if len(finder.plan.Findings) != 1 || finder.plan.Findings[0].Err == nil ||
		finder.plan.Findings[0].Err.Error() != "fleet scan: tmux missing" {
		t.Fatalf("failed scan = %+v", finder.plan.Findings)
	}
	finder = &discoverer{deps: testDeps(t, &hostRunner{}, newKernel(), &fakeFleet{
		unreadable: map[string]string{"pfm-cc-x": "permission denied"},
	})}
	finder.findChats(context.Background())
	if len(finder.plan.Findings) != 2 || finder.plan.Findings[1].Err == nil ||
		finder.plan.Findings[1].Err.Error() != "tmux socket pfm-cc-x could not be read, its chats are unknown: permission denied" {
		t.Fatalf("unreadable socket = %+v", finder.plan.Findings)
	}
	finder = &discoverer{deps: Deps{}}
	finder.findChats(context.Background())
	if finder.plan.Findings[0].Err == nil {
		t.Fatal("no fleet read as no chats")
	}
}

func TestAChatThatWillNotEndIsAFailedStep(t *testing.T) {
	fleet := &fakeFleet{
		chats:  []headless.Chat{{Engine: pfmengine.Claude, Name: "a", ID: "sess-a", Socket: "pfm-cc-a"}},
		endErr: map[string]error{"sess-a": errors.New("socket still answers")},
	}
	finder := &discoverer{deps: testDeps(t, &hostRunner{}, newKernel(), fleet)}
	finder.findChats(context.Background())
	if _, err := finder.plan.Units[0].Stop(context.Background()); err == nil {
		t.Fatal("a chat that did not end reported ended")
	}
}

func TestASupervisedDaemonBelongsToItsServiceUnit(t *testing.T) {
	var order []string
	dependencies, _, _, _ := fullHost(t, &order)
	dependencies.Services = &fakeServices{units: []Unit{serviceUnit(&order, "systemd "+mcpSystemdUnit)}}
	plan := BuildPlan(context.Background(), dependencies)
	names := unitNames(plan.Units)
	if !slices.Contains(names, "services: systemd pfm-mcp.service") {
		t.Fatalf("the daemon's unit is missing: %v", names)
	}
	for _, name := range names {
		if strings.HasPrefix(name, "mcp-daemon:") || strings.Contains(name, "pid 11") {
			t.Fatalf("a supervised daemon is stopped by pid too: %v", names)
		}
	}
	for index := range plan.Units {
		unit := &plan.Units[index]
		if unit.Name == "systemd pfm-mcp.service" && unit.StartHow != "start + probe" {
			t.Fatalf("the supervised daemon starts without its status probe: %q", unit.StartHow)
		}
	}
}

func TestFindServicesAFailedManagerIsAFailedFind(t *testing.T) {
	dependencies := testDeps(t, &hostRunner{}, newKernel(), &fakeFleet{})
	dependencies.Services = &fakeServices{err: errors.New("systemctl --user: no bus")}
	finder := &discoverer{deps: dependencies}
	if _, err := finder.findServices(context.Background()); err == nil {
		t.Fatal("a failed service manager returned no error")
	}
	if finder.plan.Findings[0].Err == nil {
		t.Fatalf("a failed service manager = %+v", finder.plan.Findings)
	}
}

func TestStartDaemonVerifiesItsStatusDocument(t *testing.T) {
	runner := &hostRunner{}
	dependencies := testDeps(t, runner, newKernel(), &fakeFleet{})
	dependencies.ConfigPath = "/jail/pfm.config.json"
	finder := &discoverer{deps: dependencies}
	if _, err := finder.startDaemon(
		context.Background(),
	); err == nil ||
		!strings.Contains(err.Error(), "no MCP server is enabled") {
		t.Fatalf("a daemon with no port to prove = %v", err)
	}
	calls := 0
	finder.deps.DaemonProbe = func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("connection refused")
		}
		return nil
	}
	detail, err := finder.startDaemon(context.Background())
	if err != nil || detail != "pid 4242 serving; /status answered" {
		t.Fatalf("start = %q, %v", detail, err)
	}
	if got := strings.Join(
		runner.starts[0].Argv,
		" ",
	); got != "/opt/pfm/bin/pfm --config /jail/pfm.config.json mcp serve" ||
		!runner.starts[0].Opts.Detach {
		t.Fatalf("start argv = %q detach %v", got, runner.starts[0].Opts.Detach)
	}
	finder.deps.DaemonProbe = func(context.Context) error { return errors.New("connection refused") }
	if _, err := finder.startDaemon(
		context.Background(),
	); err == nil ||
		!strings.Contains(err.Error(), "mcp-serve.log") {
		t.Fatalf("a daemon that never answers = %v, want its log named", err)
	}
}

// TestADaemonUnitThatStaysStoppedSupervisesNothing: when the MCP daemon's
// service unit was not up, a daemon run by hand beside it is restarted by
// pid, never left to a unit the restart does not start.
func TestADaemonUnitThatStaysStoppedSupervisesNothing(t *testing.T) {
	var order []string
	dependencies, _, _, _ := fullHost(t, &order)
	down := Unit{
		Name: "systemd " + mcpSystemdUnit, FoundBy: "fake", Before: "ActiveState=inactive", StaysStopped: true,
	}
	dependencies.Services = &fakeServices{units: []Unit{down}}
	plan := BuildPlan(context.Background(), dependencies)
	names := unitNames(plan.Units)
	if !slices.Contains(names, "mcp-daemon: pfm mcp serve pid 11") {
		t.Fatalf("a hand-run daemon beside a stopped daemon unit is not restarted by pid: %v", names)
	}
}

// TestAnUnknownSupervisorLeavesTheDaemonRunning: when the services find
// fails, whether a service unit supervises the MCP daemon is unknown; the
// daemon is neither stopped by pid nor started by hand, and the mcp-daemon
// find fails, named.
func TestAnUnknownSupervisorLeavesTheDaemonRunning(t *testing.T) {
	var order []string
	dependencies, _, _, _ := fullHost(t, &order)
	dependencies.Services = &fakeServices{err: errors.New("systemctl --user: no bus")}
	plan := BuildPlan(context.Background(), dependencies)
	for _, name := range unitNames(plan.Units) {
		if strings.Contains(name, "pid 11") {
			t.Fatalf("a daemon whose supervisor is unknown is in the plan: %v", unitNames(plan.Units))
		}
	}
	var daemonErr error
	for _, finding := range plan.Findings {
		if finding.Family == FamilyMCPDaemon && finding.Err != nil {
			daemonErr = finding.Err
		}
	}
	if daemonErr == nil ||
		daemonErr.Error() != "the services find failed, so whether a service unit supervises the daemon is unknown — 1 pfm mcp serve process(es) left running: systemctl --user: no bus" {
		t.Fatalf("mcp-daemon find with its supervisor unknown = %v, want a failed find naming the daemon", daemonErr)
	}
}
