package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/agentlauncher"
	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

// TestScheduleUnitsStageRendered: install stages the schedule timer — the
// fixed 5-minute wall-clock tick, persistent across sleep — and its oneshot
// service, which fires the due schedules on the one service PATH, with no
// install-time marker left in either.
func TestScheduleUnitsStageRendered(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if _, err := Run(context.Background(), Options{
		scheduler:     schedulerFor(servicemanager.KindSystemd),
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	for unit, want := range map[string]string{
		"pfm-schedule.timer": "[Unit]\nDescription=Fire due pfm schedules every 5 minutes\n\n[Timer]\nOnCalendar=*:0/5\n" +
			"Persistent=true\nUnit=pfm-schedule.service\n\n[Install]\nWantedBy=timers.target",
		scheduleServiceUnit: "[Unit]\nDescription=Fire every due pfm schedule (pfm internal schedule-fire)\n\n[Service]\n" +
			"Type=oneshot\nEnvironment=PATH={home}/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin\n" +
			"Environment=PFM_INSTALL_MARK={mark}\nExecStart=%h/.local/bin/pfm internal schedule-fire",
	} {
		staged := filepath.Join(home, ".local", "share", "pfm", "install", "systemd", unit)
		content, err := os.ReadFile(staged)
		if err != nil {
			t.Fatalf("read staged %s: %v", unit, err)
		}
		if got := serviceUnitBody(content, home); got != want {
			t.Errorf("%s =\n%s\nwant\n%s", staged, got, want)
		}
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
	wantArgv := "<string>" + agentlauncher.Program(home) + "</string>\n" +
		"\t\t<string>internal</string>\n" +
		"\t\t<string>schedule-fire</string>"
	if !strings.Contains(plist, wantArgv) {
		t.Errorf("schedule launch agent argv is not this home's launcher running internal schedule-fire:\n%s", plist)
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

// TestUnwireScheduleLaunchAgentRemovesOnlyAnUnloadedAgent: unwire removes the
// plist once launchd no longer holds the job — a bootout that succeeded, or
// one that failed because the label is already gone, at once or after a
// teardown still in flight — and keeps it, saying why, while the job stays
// loaded or launchctl never answered, so the next run retries rather than
// orphaning a loaded job no plist names.
func TestUnwireScheduleLaunchAgentRemovesOnlyAnUnloadedAgent(t *testing.T) {
	t.Parallel()
	notFound := commandExitError{name: "launchctl", code: 113, text: "Could not find service"}
	refused := commandExitError{name: "launchctl", code: 5, text: "Boot-out failed: 5: Input/output error"}
	missing := errors.New(`run "launchctl": exec: "launchctl": executable file not found in $PATH`)
	for _, test := range []struct {
		name    string
		bootout error
		prints  []error
		checks  int
		want    string
	}{
		{name: "bootout unloads", prints: []error{nil}},
		{name: "label already gone", bootout: notFound, prints: []error{notFound}, checks: 1},
		{name: "teardown in flight", bootout: refused, prints: []error{nil, nil, notFound}, checks: 3},
		{
			name: "still loaded", bootout: refused, prints: []error{nil}, checks: 20,
			want: "remove schedule launch agent PLIST: unload com.professor.pfm.schedule before removing PLIST: " +
				"launchctl bootout TARGET failed and the job is still loaded after 20 checks: " +
				"launchctl exited 5: Boot-out failed: 5: Input/output error",
		},
		{
			name: "launchctl missing", bootout: missing,
			want: "remove schedule launch agent PLIST: unload com.professor.pfm.schedule before removing PLIST: " +
				`launchctl bootout TARGET could not run: run "launchctl": exec: "launchctl": executable file not found in $PATH`,
		},
		{
			name: "print cannot confirm", bootout: refused, prints: []error{missing}, checks: 1,
			want: "remove schedule launch agent PLIST: unload com.professor.pfm.schedule before removing PLIST: " +
				"launchctl bootout TARGET failed (launchctl exited 5: Boot-out failed: 5: Input/output error) " +
				`and launchctl print could not confirm it unloaded: run "launchctl": exec: "launchctl": executable file not found in $PATH`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			plist := filepath.Join(home, "Library", "LaunchAgents", scheduleLaunchdLabel+".plist")
			target := "gui/" + strconv.Itoa(os.Getuid()) + "/" + scheduleLaunchdLabel
			writeFixture(t, plist, "<plist/>\n")
			runner := &bootoutRunner{bootout: test.bootout, prints: test.prints}
			installer := engine{
				options: Options{Home: home, Runner: runner, Stdout: io.Discard, Sleep: func(time.Duration) {}},
				apply:   true,
				stamp:   "test",
			}
			err := installer.unwireScheduleLaunchAgentLabel(context.Background(), scheduleLaunchdLabel)
			want := strings.NewReplacer("PLIST", plist, "TARGET", target).Replace(test.want)
			if got := fmt.Sprint(err); (err == nil) != (want == "") || (err != nil && got != want) {
				t.Fatalf("unwire error = %v\nwant %q", err, want)
			}
			wantCalls := []string{"launchctl bootout " + target}
			for range test.checks {
				wantCalls = append(wantCalls, "launchctl print "+target)
			}
			if strings.Join(runner.calls, "\n") != strings.Join(wantCalls, "\n") {
				t.Fatalf("calls = %q\nwant %q", runner.calls, wantCalls)
			}
			_, statErr := os.Lstat(plist)
			if kept := statErr == nil; kept != (want != "") {
				t.Fatalf("plist %s kept=%t (stat: %v), want kept=%t", plist, kept, statErr, want != "")
			}
		})
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
			scheduler:     schedulerFor(servicemanager.KindSystemd),
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
	for _, kind := range schedulerKinds {
		launchd := kind == servicemanager.KindLaunchd
		scenarios := []string{
			"no drift",
			"drifted unit",
			"nothing installed",
			"armed none",
			"unreadable unit",
			"no home",
		}
		if !launchd {
			// Only systemd units are links into the managed root.
			scenarios = append(scenarios, "dangling unit link", "missing service")
		}
		for _, scenario := range scenarios {
			t.Run(string(kind)+" "+scenario, func(t *testing.T) {
				home := t.TempDir()
				path := filepath.Join(home, ".config", "systemd", "user", scheduleTimerUnit)
				if launchd {
					path = filepath.Join(home, "Library", "LaunchAgents", scheduleLaunchdLabel+".plist")
				}
				if scenario == "no drift" || scenario == "drifted unit" || scenario == "dangling unit link" ||
					scenario == "missing service" {
					if launchd {
						installer := engine{apply: true, stamp: "test", options: Options{
							Home: home, Runner: &fakeRunner{}, Stdout: io.Discard, Sleep: func(time.Duration) {},
							scheduler: schedulerFor(kind),
						}}
						if err := installer.wireScheduleLaunchAgent(context.Background()); err != nil {
							t.Fatal(err)
						}
					} else {
						if _, err := Run(context.Background(), Options{
							Mode: ModeApply, Home: home, MCPConfigPath: testConfigPath(t),
							Runner: &fakeRunner{manager: true}, Stdout: io.Discard,
							scheduler: schedulerFor(kind),
						}); err != nil {
							t.Fatal(err)
						}
					}
				}
				if scenario == "drifted unit" {
					if !launchd {
						path = filepath.Join(home, ".config", "systemd", "user", scheduleServiceUnit)
					}
					if err := os.WriteFile(path, []byte("stale\n"), 0o644); err != nil {
						t.Fatal(err)
					}
					if !launchd {
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
				gotPath, drifted, err := scheduleFilesDrift(schedulerFor(kind), home, scenario == "armed none")
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
