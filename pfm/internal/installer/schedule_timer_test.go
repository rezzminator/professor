package installer

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The schedule tick is fixed at five minutes: the systemd timer and the launchd
// StartInterval must name the same cadence, and neither carries an install-time
// marker.
func TestScheduleTimerAssetCarriesTheFiveMinuteTick(t *testing.T) {
	t.Parallel()
	timer, err := readAsset("systemd/" + scheduleTimerUnit)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"OnCalendar=*:0/5",
		"Persistent=true",
		"Unit=pfm-schedule.service",
		"WantedBy=timers.target",
	} {
		if !strings.Contains(string(timer), want) {
			t.Errorf("schedule timer asset is missing %q:\n%s", want, timer)
		}
	}
	if strings.Contains(string(timer), "__PFM_") {
		t.Errorf("schedule timer asset carries an install-time marker:\n%s", timer)
	}
}

func TestScheduleServiceAssetFiresAndStagesWithoutMarkers(t *testing.T) {
	t.Parallel()
	service, err := readAsset("systemd/" + scheduleServiceUnit)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Type=oneshot",
		"ExecStart=%h/.local/bin/pfm internal schedule-fire",
	} {
		if !strings.Contains(string(service), want) {
			t.Errorf("schedule service asset is missing %q:\n%s", want, service)
		}
	}
	if schedulerIsLaunchd {
		return
	}
	home := t.TempDir()
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(home, ".local", "share", "pfm", "install", "systemd", scheduleServiceUnit)
	content, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("read staged schedule service: %v", err)
	}
	if strings.Contains(string(content), "__PFM_") {
		t.Fatalf("staged schedule service keeps an unrendered marker:\n%s", content)
	}
	if !strings.Contains(string(content), "Environment=PATH="+paths.ServicePath(home)) {
		t.Fatalf("staged schedule service does not carry the service PATH:\n%s", content)
	}
}

func TestWireScheduleLaunchAgentWritesTheFiveMinuteAgent(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	installer := engine{
		options: Options{
			Home:   home,
			Runner: &fakeRunner{},
			Stdout: io.Discard,
			Sleep:  func(time.Duration) {},
		},
		apply: true,
		stamp: "test",
	}
	if err := installer.wireScheduleLaunchAgent(context.Background()); err != nil {
		t.Fatalf("wireScheduleLaunchAgent: %v", err)
	}
	path := filepath.Join(home, "Library", "LaunchAgents", scheduleLaunchdLabel+".plist")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("schedule launch agent was not written: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("schedule launch agent is a symlink; launchd will never load it")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plist := string(content)
	if strings.Contains(plist, "__PFM_") {
		t.Errorf("schedule launch agent keeps an unrendered marker:\n%s", plist)
	}
	wantArgv := "<string>" + home + "/.local/bin/pfm</string>\n" +
		"\t\t<string>internal</string>\n" +
		"\t\t<string>schedule-fire</string>"
	if !strings.Contains(plist, wantArgv) {
		t.Errorf("schedule launch agent argv is not this home's pfm internal schedule-fire:\n%s", plist)
	}
	if !strings.Contains(plist, "<key>StartInterval</key>\n\t<integer>300</integer>") {
		t.Errorf("schedule launch agent does not tick every 300 s:\n%s", plist)
	}

	// A second wire is a no-op, and unwire removes the agent.
	if err := installer.wireScheduleLaunchAgent(context.Background()); err != nil {
		t.Fatalf("second wireScheduleLaunchAgent: %v", err)
	}
	if err := installer.unwireScheduleLaunchAgent(context.Background()); err != nil {
		t.Fatalf("unwireScheduleLaunchAgent: %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("unwire left the schedule launch agent at %s: %v", path, err)
	}
	if err := installer.unwireScheduleLaunchAgent(context.Background()); err != nil {
		t.Fatalf("unwire of an absent schedule launch agent: %v", err)
	}
}

func TestScheduleUnitsAreManagedAndEnabled(t *testing.T) {
	t.Parallel()
	for _, unit := range []string{scheduleServiceUnit, scheduleTimerUnit} {
		if !slices.Contains(unitNames, unit) {
			t.Errorf("unitNames = %v, want it to contain %q", unitNames, unit)
		}
	}
	found := false
	for _, enablement := range unitEnablements {
		if enablement.unit == scheduleTimerUnit && enablement.wants == "timers.target.wants" {
			found = true
		}
	}
	if !found {
		t.Errorf("unitEnablements = %v, want %q under timers.target.wants", unitEnablements, scheduleTimerUnit)
	}
}

// Install, uninstall and reinstall leave the schedule pair exactly as name-sync's
// is left: linked and enabled, gone, then linked and enabled again.
func TestScheduleUnitsSurviveInstallUninstallReinstall(t *testing.T) {
	t.Parallel()
	if schedulerIsLaunchd {
		t.Skip("systemd units are not installed on a launchd host")
	}
	home := t.TempDir()
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	managed := filepath.Join(home, ".local", "share", "pfm", "install")
	serviceLink := filepath.Join(unitDir, scheduleServiceUnit)
	timerLink := filepath.Join(unitDir, scheduleTimerUnit)
	enableLink := filepath.Join(unitDir, "timers.target.wants", scheduleTimerUnit)
	links := map[string]string{
		serviceLink: filepath.Join(managed, "systemd", scheduleServiceUnit),
		timerLink:   filepath.Join(managed, "systemd", scheduleTimerUnit),
		enableLink:  timerLink,
	}
	run := func(mode Mode) {
		t.Helper()
		var output strings.Builder
		if _, err := Run(context.Background(), Options{
			MCPConfigPath: testConfigPath(t),
			Mode:          mode, Home: home, Stdout: &output, Runner: &fakeRunner{manager: true},
		}); err != nil {
			t.Fatalf("mode %d: %v\n%s", mode, err, output.String())
		}
	}
	run(ModeApply)
	for link, want := range links {
		assertLink(t, link, want)
	}
	run(ModeUninstall)
	for link := range links {
		if _, err := os.Lstat(link); !os.IsNotExist(err) {
			t.Fatalf("uninstall left %s: %v", link, err)
		}
	}
	run(ModeApply)
	for link, want := range links {
		assertLink(t, link, want)
	}
}

func TestScheduleTimerDrift(t *testing.T) {
	scenarios := []string{"no drift", "drifted unit", "nothing installed", "armed none", "unreadable unit", "no home"}
	if !schedulerIsLaunchd {
		// Only systemd units are links into the managed root.
		scenarios = append(scenarios, "dangling unit link", "missing service")
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, ".config", "systemd", "user", scheduleTimerUnit)
			if schedulerIsLaunchd {
				path = filepath.Join(home, "Library", "LaunchAgents", scheduleLaunchdLabel+".plist")
			}
			if scenario == "no drift" || scenario == "drifted unit" || scenario == "dangling unit link" ||
				scenario == "missing service" {
				if schedulerIsLaunchd {
					installer := engine{apply: true, stamp: "test", options: Options{
						Home: home, Runner: &fakeRunner{}, Stdout: io.Discard, Sleep: func(time.Duration) {},
					}}
					if err := installer.wireScheduleLaunchAgent(context.Background()); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := Run(context.Background(), Options{
						Mode: ModeApply, Home: home, MCPConfigPath: testConfigPath(t),
						Runner: &fakeRunner{manager: true}, Stdout: io.Discard,
					}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if scenario == "drifted unit" {
				if !schedulerIsLaunchd {
					path = filepath.Join(home, ".config", "systemd", "user", scheduleServiceUnit)
				}
				if err := os.WriteFile(path, []byte("stale\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if !schedulerIsLaunchd {
					target, err := os.Readlink(path)
					if err != nil {
						t.Fatalf("drift fixture must retain the installed symlink: %v", err)
					}
					if raw, err := os.ReadFile(target); err != nil || string(raw) != "stale\n" {
						t.Fatalf("link target=%q err=%v", raw, err)
					}
				}
			}
			if scenario == "missing service" {
				path = filepath.Join(home, ".config", "systemd", "user", scheduleServiceUnit)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "dangling unit link" {
				target, err := os.Readlink(path)
				if err != nil {
					t.Fatalf("dangling fixture must start from the installed symlink: %v", err)
				}
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "unreadable unit" {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "no home" {
				home = ""
			}
			gotPath, drifted, err := ScheduleTimerDrift(home, scenario == "armed none")
			switch scenario {
			case "no home":
				if err == nil || err.Error() != "schedule timer drift: no home directory" || drifted ||
					gotPath != "" {
					t.Fatalf("path=%q drifted=%t err=%v", gotPath, drifted, err)
				}
			case "unreadable unit":
				if err == nil || !strings.HasPrefix(err.Error(), "read "+path+":") || drifted || gotPath != "" {
					t.Fatalf("path=%q drifted=%t err=%v", gotPath, drifted, err)
				}
			case "drifted unit", "dangling unit link", "missing service", "armed none":
				if err != nil || !drifted || gotPath != path {
					t.Fatalf("path=%q drifted=%t err=%v want drift=%s", gotPath, drifted, err, path)
				}
			default:
				if err != nil || drifted || gotPath != "" {
					t.Fatalf("path=%q drifted=%t err=%v", gotPath, drifted, err)
				}
			}
		})
	}
}

// TestWireScheduleLaunchAgentRetiresTheRenamedAgent: the agent the schedule
// agent was renamed from is booted out and removed by install and uninstall.
func TestWireScheduleLaunchAgentRetiresTheRenamedAgent(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	runner := &fakeRunner{}
	installer := engine{
		options: Options{Home: home, Runner: runner, Stdout: io.Discard, Sleep: func(time.Duration) {}},
		apply:   true,
		stamp:   "test",
	}
	retired := filepath.Join(home, "Library", "LaunchAgents", retiredScheduleLaunchdLabel+".plist")
	for _, wire := range []func(context.Context) error{
		installer.wireScheduleLaunchAgent, installer.unwireScheduleLaunchAgent,
	} {
		writeFixture(t, retired, "<plist/>\n")
		runner.calls = nil
		if err := wire(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(retired); !os.IsNotExist(err) {
			t.Fatalf("the renamed launch agent remains at %s: %v", retired, err)
		}
		if !slices.ContainsFunc(runner.calls, func(call string) bool {
			return strings.HasPrefix(call, "launchctl bootout ") &&
				strings.HasSuffix(call, "/"+retiredScheduleLaunchdLabel)
		}) {
			t.Fatalf("the renamed launch agent was never booted out: %q", runner.calls)
		}
	}
}
