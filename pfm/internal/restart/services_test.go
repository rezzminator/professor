package restart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

const keepAlivePlist = "<plist><dict><key>KeepAlive</key>\n  <true/></dict></plist>"

func writeFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		body := "<plist/>"
		if strings.HasSuffix(name, "mcp.plist") {
			body = keepAlivePlist
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// launchd models launchctl over a set of loaded labels; a label in sticky
// never boots out, one in stuck never bootstraps.
type launchd struct {
	mu     sync.Mutex
	loaded map[string]bool
	sticky map[string]bool
	stuck  map[string]bool
}

func (model *launchd) run(argv []string) (deps.RunResult, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	switch argv[1] {
	case "print":
		label := filepath.Base(argv[2])
		if !model.loaded[label] {
			return exitWith(launchctlNotFound, "Could not find service")
		}
		return answer("\tstate = running\n")
	case "bootout":
		label := filepath.Base(argv[2])
		if !model.sticky[label] {
			delete(model.loaded, label)
		}
		return answer("")
	case "bootstrap":
		label := strings.TrimSuffix(filepath.Base(argv[3]), ".plist")
		if model.stuck[label] {
			return exitWith(5, "Bootstrap failed: 5: Input/output error")
		}
		model.loaded[label] = true
		return answer("")
	}
	return exitWith(64, "unknown launchctl verb")
}

func newLaunchd(t *testing.T, loaded ...string) (LaunchdManager, *launchd, *hostRunner) {
	t.Helper()
	home := t.TempDir()
	writeFiles(t, filepath.Join(home, "Library", "LaunchAgents"),
		"com.professor.pfm.schedule.plist", "com.professor.pfm.mcp.plist", "com.example.other.plist")
	model := &launchd{loaded: map[string]bool{}, sticky: map[string]bool{}, stuck: map[string]bool{}}
	for _, label := range loaded {
		model.loaded[label] = true
	}
	runner := &hostRunner{handle: model.run, paths: map[string]string{"launchctl": "/bin/launchctl"}}
	return NewLaunchdManager(runner, newTickClock(), home, 501), model, runner
}

func pfmService(name string) bool {
	return strings.HasPrefix(name, pfmLaunchdPrefix) || strings.HasPrefix(name, pfmSystemdPrefix)
}

func TestLaunchdUnitsFindPfmAgentsWithTheDaemonStartingFirst(t *testing.T) {
	manager, _, _ := newLaunchd(t, "com.professor.pfm.mcp")
	units, detail, err := manager.Units(context.Background(), FamilyServices, pfmService, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := unitNames(units)
	want := []string{"services: launchd com.professor.pfm.schedule", "services: launchd com.professor.pfm.mcp"}
	if !slices.Equal(got, want) {
		t.Fatalf("units = %v, want %v (the KeepAlive daemon last, so it starts first)", got, want)
	}
	if !strings.Contains(detail, "2 launch agent(s)") || !strings.Contains(detail, "gui/501") {
		t.Fatalf("detail = %q", detail)
	}
	if !strings.HasSuffix(units[1].FoundBy, ", loaded") || !strings.HasSuffix(units[0].FoundBy, ", not loaded") {
		t.Fatalf("found-by lines do not read launchd's state: %q / %q", units[0].FoundBy, units[1].FoundBy)
	}
	if !strings.Contains(units[1].StartHow, "state = running") {
		t.Fatalf("the KeepAlive agent's start is not verified running: %q", units[1].StartHow)
	}
}

func TestLaunchdStopAndStartVerifyEachState(t *testing.T) {
	manager, model, _ := newLaunchd(t, "com.professor.pfm.mcp")
	probed := 0
	units, _, err := manager.Units(
		context.Background(),
		FamilyServices,
		pfmService,
		func(name string) func(context.Context) error {
			if name != "com.professor.pfm.mcp" {
				return nil
			}
			return func(context.Context) error { probed++; return nil }
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if !units[0].StaysStopped || units[0].Stop != nil || units[0].Start != nil {
		t.Fatalf("an agent not loaded before is stopped or started: %+v", units[0])
	}
	if detail, err := units[1].Stop(ctx); err != nil || !strings.HasPrefix(detail, "booted out") {
		t.Fatalf("stop = %q, %v", detail, err)
	}
	if model.loaded["com.professor.pfm.mcp"] {
		t.Fatal("the agent is still loaded after its stop said booted out")
	}
	detail, err := units[1].Start(ctx)
	if err != nil || detail != "loaded, state = running, health probe answered" || probed != 1 {
		t.Fatalf("start = %q, %v (probed %d)", detail, err, probed)
	}
}

// answersOnThirdProbe is a health probe for the unit named want that refuses
// twice, then answers: a daemon its manager reads up before it serves.
func answersOnThirdProbe(want string) Verifier {
	calls := 0
	return func(name string) func(context.Context) error {
		if name != want {
			return nil
		}
		return func(context.Context) error {
			calls++
			if calls < 3 {
				return errors.New("connection refused")
			}
			return nil
		}
	}
}

func TestLaunchdStartWaitsForTheHealthProbe(t *testing.T) {
	manager, _, _ := newLaunchd(t, "com.professor.pfm.mcp")
	units, _, err := manager.Units(
		context.Background(), FamilyServices, pfmService, answersOnThirdProbe("com.professor.pfm.mcp"))
	if err != nil {
		t.Fatal(err)
	}
	if detail, err := units[1].Start(context.Background()); err != nil || !strings.HasSuffix(detail, "probe answered") {
		t.Fatalf("start of a daemon that answers on its third probe = %q, %v", detail, err)
	}
}

func TestSystemdStartWaitsForTheHealthProbe(t *testing.T) {
	manager, _ := newSystemd(t)
	units, _, err := manager.Units(
		context.Background(), FamilyServices, pfmService, answersOnThirdProbe("pfm-mcp.service"))
	if err != nil {
		t.Fatal(err)
	}
	if detail, err := units[5].Start(context.Background()); err != nil || !strings.HasSuffix(detail, "probe answered") {
		t.Fatalf("start of a daemon that answers on its third probe = %q, %v", detail, err)
	}
}

func TestLaunchdFailuresAreNamed(t *testing.T) {
	manager, model, _ := newLaunchd(t, "com.professor.pfm.mcp", "com.professor.pfm.schedule")
	model.sticky["com.professor.pfm.mcp"] = true
	model.stuck["com.professor.pfm.schedule"] = true
	units, _, err := manager.Units(
		context.Background(),
		FamilyServices,
		pfmService,
		func(string) func(context.Context) error {
			return func(context.Context) error { return errors.New("connection refused") }
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := units[1].Stop(ctx); err == nil || !strings.Contains(err.Error(), "still loaded") {
		t.Fatalf("a bootout that did not take = %v", err)
	}
	if _, err := units[0].Stop(ctx); err != nil {
		t.Fatalf("stop = %v", err)
	}
	if _, err := units[0].Start(ctx); err == nil || !strings.Contains(err.Error(), "Input/output error") {
		t.Fatalf("a bootstrap that did not take = %v", err)
	}
	if _, err := units[1].Start(
		ctx,
	); err == nil ||
		!strings.Contains(err.Error(), "health probe failed: connection refused") {
		t.Fatalf("a failing health probe = %v", err)
	}
}

func TestLaunchdAbsentIsNamedAndALookupFailureIsAnError(t *testing.T) {
	manager, _, runner := newLaunchd(t)
	delete(runner.paths, "launchctl")
	units, detail, err := manager.Units(context.Background(), FamilyServices, pfmService, nil)
	if err != nil || len(units) != 0 || !strings.Contains(detail, "no launchctl") {
		t.Fatalf("no launchctl = %v, %q, %v", units, detail, err)
	}
	runner.lookErr = map[string]error{"launchctl": errors.New("permission denied")}
	if _, _, err := manager.Units(context.Background(), FamilyServices, pfmService, nil); err == nil {
		t.Fatal("a failed lookup of launchctl read as no launchctl")
	}
	runner.lookErr = nil
	runner.paths["launchctl"] = "/bin/launchctl"
	runner.handle = func([]string) (deps.RunResult, error) { return exitWith(1, "launchd is wedged") }
	if _, _, err := manager.Units(context.Background(), FamilyServices, pfmService, nil); err == nil ||
		!strings.Contains(err.Error(), "launchd is wedged") {
		t.Fatalf("an unreadable launchctl print = %v", err)
	}
}

// systemd models systemctl --user over unit states; a unit in broken never
// reaches active.
type systemd struct {
	mu     sync.Mutex
	state  map[string]string
	broken map[string]bool
}

func (model *systemd) run(argv []string) (deps.RunResult, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	name := argv[len(argv)-1]
	switch argv[2] {
	case "show":
		state, ok := model.state[name]
		if !ok {
			state = "inactive"
		}
		return answer(state + "\n")
	case "stop":
		model.state[name] = "inactive"
		return answer("")
	case "start":
		if model.broken[name] {
			model.state[name] = "failed"
			return exitWith(1, "Job failed")
		}
		model.state[name] = "active"
		return answer("")
	}
	return exitWith(64, "unknown systemctl verb")
}

func newSystemd(t *testing.T) (SystemdManager, *systemd) {
	t.Helper()
	home := t.TempDir()
	writeFiles(t, filepath.Join(home, ".config", "systemd", "user"),
		"pfm-mcp.service", "pfm-name-sync.path", "pfm-name-sync.service", "pfm-name-sync.timer",
		"pfm-schedule.service", "pfm-schedule.timer", "other.service")
	model := &systemd{state: map[string]string{"pfm-mcp.service": "active"}, broken: map[string]bool{}}
	runner := &hostRunner{handle: model.run, paths: map[string]string{"systemctl": "/bin/systemctl"}}
	return NewSystemdManager(runner, newTickClock(), home), model
}

func TestSystemdUnitsOrderTriggersFirstAndStartTheDaemonFirst(t *testing.T) {
	manager, model := newSystemd(t)
	model.state["pfm-name-sync.path"] = "active"
	model.state["pfm-name-sync.service"] = "active"
	units, detail, err := manager.Units(context.Background(), FamilyServices, pfmService, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"services: systemd pfm-name-sync.path",
		"services: systemd pfm-name-sync.timer",
		"services: systemd pfm-schedule.timer",
		"services: systemd pfm-name-sync.service",
		"services: systemd pfm-schedule.service",
		"services: systemd pfm-mcp.service",
	}
	if got := unitNames(units); !slices.Equal(got, want) {
		t.Fatalf("units =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if units[3].Start != nil || !strings.Contains(units[3].StopHow, "its timer or path unit starts it again") {
		t.Fatalf("a triggered service is started directly: %+v", units[3].StopHow)
	}
	if units[5].Start == nil || !strings.Contains(units[5].FoundBy, "ActiveState=active") {
		t.Fatalf("the daemon unit = %+v", units[5].FoundBy)
	}
	if !strings.Contains(detail, "6 user unit(s)") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestSystemdStopAndStartVerifyActiveState(t *testing.T) {
	manager, model := newSystemd(t)
	model.state["pfm-schedule.timer"] = "active"
	units, _, err := manager.Units(
		context.Background(),
		FamilyServices,
		pfmService,
		func(name string) func(context.Context) error {
			if name == "pfm-mcp.service" {
				return func(context.Context) error { return nil }
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	daemon := units[5]
	if detail, err := daemon.Stop(ctx); err != nil || detail != "ActiveState=inactive" {
		t.Fatalf("stop = %q, %v", detail, err)
	}
	if detail, err := daemon.Start(ctx); err != nil || detail != "ActiveState=active, health probe answered" {
		t.Fatalf("start = %q, %v", detail, err)
	}
	model.broken["pfm-schedule.timer"] = true
	if _, err := units[2].Start(
		ctx,
	); err == nil ||
		!strings.Contains(err.Error(), "journalctl --user -u pfm-schedule.timer") {
		t.Fatalf("a unit that never comes up = %v", err)
	}
}

func TestInstalledNamesReadsAMissingDirAsNothingInstalled(t *testing.T) {
	names, err := installedNames(filepath.Join(t.TempDir(), "absent"), func(string) bool { return true })
	if err != nil || names != nil {
		t.Fatalf("missing dir = %v, %v", names, err)
	}
	file := filepath.Join(t.TempDir(), "file")
	writeFiles(t, filepath.Dir(file), "file")
	if _, err := installedNames(file, func(string) bool { return true }); err == nil {
		t.Fatal("a dir that cannot be read read as nothing installed")
	}
}

func TestLaunchdKeepAliveReadsThePlist(t *testing.T) {
	if !launchdKeepAlive(keepAlivePlist) || launchdKeepAlive("<key>KeepAlive</key><false/>") ||
		launchdKeepAlive("<plist/>") {
		t.Fatal("launchdKeepAlive misread a plist")
	}
}

func TestNativeServiceManagerIsLaunchdOnMacOSAndSystemdOnLinux(t *testing.T) {
	home := t.TempDir()
	launchdManager, ok := nativeServiceManager("darwin", &hostRunner{}, newTickClock(), home, 501).(LaunchdManager)
	if !ok || launchdManager.Dir != filepath.Join(home, "Library", "LaunchAgents") || launchdManager.UID != 501 {
		t.Fatalf("darwin manager = %#v, want launchd over the LaunchAgents dir", launchdManager)
	}
	systemdManager, ok := nativeServiceManager("linux", &hostRunner{}, newTickClock(), home, 501).(SystemdManager)
	if !ok || systemdManager.Dir != filepath.Join(home, ".config", "systemd", "user") {
		t.Fatalf("linux manager = %#v, want systemd over the user unit dir", systemdManager)
	}
	if manager := nativeServiceManager("freebsd", &hostRunner{}, newTickClock(), home, 501); manager != nil {
		t.Fatalf("freebsd manager = %#v, want none", manager)
	}
}

// TestLaunchdAnAgentNotLoadedBeforeStaysStopped: an agent that was not loaded
// when the plan was built is neither booted out nor bootstrapped, and both
// the plan and the result lines name it; the loaded one restarts.
func TestLaunchdAnAgentNotLoadedBeforeStaysStopped(t *testing.T) {
	manager, model, runner := newLaunchd(t, "com.professor.pfm.mcp")
	units, _, err := manager.Units(context.Background(), FamilyServices, pfmService, nil)
	if err != nil {
		t.Fatal(err)
	}
	printed, executed, code := printAndRun(t, units)
	if code != 0 {
		t.Fatalf("Execute = %d, want 0:\n%s", code, executed)
	}
	for _, argv := range runner.ran() {
		if argv[1] != "print" && strings.Contains(strings.Join(argv, " "), "com.professor.pfm.schedule") {
			t.Fatalf("an agent that was not loaded before was touched: %v", argv)
		}
	}
	if model.loaded["com.professor.pfm.schedule"] {
		t.Fatal("an agent that was not loaded before is loaded after the restart")
	}
	if !model.loaded["com.professor.pfm.mcp"] {
		t.Fatal("the agent that was loaded before is not loaded after the restart")
	}
	assertStaysStopped(t, printed, executed, "launchd com.professor.pfm.schedule", "not loaded")
	if !strings.Contains(printed, "launchd com.professor.pfm.mcp  restart (was up: loaded)") {
		t.Fatalf("the loaded agent is not planned as a restart:\n%s", printed)
	}
}

// TestSystemdAUnitInactiveBeforeStaysStopped: a timer, path or service unit
// that was not active when the plan was built is neither stopped nor started.
func TestSystemdAUnitInactiveBeforeStaysStopped(t *testing.T) {
	manager, model := newSystemd(t)
	model.state["pfm-name-sync.path"] = "active"
	units, _, err := manager.Units(context.Background(), FamilyServices, pfmService, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := manager.runner.(*hostRunner)
	printed, executed, code := printAndRun(t, units)
	if code != 0 {
		t.Fatalf("Execute = %d, want 0:\n%s", code, executed)
	}
	for _, argv := range runner.ran() {
		if argv[2] != "show" &&
			slices.Contains([]string{"pfm-schedule.timer", "pfm-name-sync.timer"}, argv[len(argv)-1]) {
			t.Fatalf("a unit that was inactive before was touched: %v", argv)
		}
	}
	if model.state["pfm-schedule.timer"] == "active" || model.state["pfm-name-sync.timer"] == "active" {
		t.Fatalf("a timer that was inactive before is active after the restart: %v", model.state)
	}
	if model.state["pfm-mcp.service"] != "active" || model.state["pfm-name-sync.path"] != "active" {
		t.Fatalf("a unit that was active before is not active after the restart: %v", model.state)
	}
	assertStaysStopped(t, printed, executed, "systemd pfm-schedule.timer", "ActiveState=inactive")
	assertStaysStopped(t, printed, executed, "systemd pfm-name-sync.timer", "ActiveState=inactive")
}

// TestSystemdAnUnreadableActiveStateFailsThePlan: a state word systemd's
// documentation does not list is a failed read, never "stopped".
func TestSystemdAnUnreadableActiveStateFailsThePlan(t *testing.T) {
	manager, model := newSystemd(t)
	model.state["pfm-schedule.timer"] = "bewildered"
	if _, _, err := manager.Units(context.Background(), FamilyServices, pfmService, nil); err == nil ||
		!strings.Contains(err.Error(), `"bewildered"`) {
		t.Fatalf("an unknown ActiveState = %v, want the plan failed with the word named", err)
	}
}

// TestSystemdATriggeredServiceThatWasUpComesBack: a triggered service that was
// up beside a trigger that stays stopped starts directly, or nothing would
// bring it back; one whose trigger was up is left to that trigger.
func TestSystemdATriggeredServiceThatWasUpComesBack(t *testing.T) {
	manager, model := newSystemd(t)
	model.state["pfm-schedule.service"] = "active"
	model.state["pfm-name-sync.path"] = "active"
	model.state["pfm-name-sync.service"] = "active"
	units, _, err := manager.Units(context.Background(), FamilyServices, pfmService, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := manager.runner.(*hostRunner)
	printed, executed, code := printAndRun(t, units)
	if code != 0 {
		t.Fatalf("Execute = %d, want 0:\n%s", code, executed)
	}
	if model.state["pfm-schedule.service"] != "active" {
		t.Fatalf("a service that was up beside a trigger that stays stopped is down after the restart:\n%s", executed)
	}
	if model.state["pfm-schedule.timer"] == "active" {
		t.Fatalf("a timer that was inactive before is active after the restart:\n%s", executed)
	}
	if !strings.Contains(printed, "systemd pfm-name-sync.service") ||
		!strings.Contains(printed, "(its timer or path unit starts it again)") {
		t.Fatalf("a service whose trigger was up is not left to it:\n%s", printed)
	}
	for _, argv := range runner.ran() {
		if argv[2] == "start" && argv[len(argv)-1] == "pfm-name-sync.service" {
			t.Fatalf("a service whose trigger was up is started directly: %v", argv)
		}
	}
}

// TestSystemdATriggeredServiceInactiveBeforeStaysStopped: a triggered service
// that was not up is neither stopped nor started, whatever its trigger.
func TestSystemdATriggeredServiceInactiveBeforeStaysStopped(t *testing.T) {
	manager, model := newSystemd(t)
	model.state["pfm-schedule.timer"] = "active"
	units, _, err := manager.Units(context.Background(), FamilyServices, pfmService, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := manager.runner.(*hostRunner)
	printed, executed, _ := printAndRun(t, units)
	for _, argv := range runner.ran() {
		if argv[2] != "show" && argv[len(argv)-1] == "pfm-schedule.service" {
			t.Fatalf("a triggered service that was inactive before was touched: %v", argv)
		}
	}
	assertStaysStopped(t, printed, executed, "systemd pfm-schedule.service", "ActiveState=inactive")
}

// TestSystemdAUnitInMaintenanceStaysStopped: maintenance is a documented
// ActiveState of a unit not running (a clean in progress), never a failed read.
func TestSystemdAUnitInMaintenanceStaysStopped(t *testing.T) {
	manager, model := newSystemd(t)
	model.state["pfm-schedule.timer"] = "maintenance"
	units, _, err := manager.Units(context.Background(), FamilyServices, pfmService, nil)
	if err != nil {
		t.Fatalf("a unit in maintenance failed the plan: %v", err)
	}
	printed, executed, _ := printAndRun(t, units)
	assertStaysStopped(t, printed, executed, "systemd pfm-schedule.timer", "ActiveState=maintenance")
}
