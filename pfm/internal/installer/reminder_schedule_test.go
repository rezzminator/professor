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
)

// The reminder tick is fixed at five minutes: the systemd timer and the launchd
// StartInterval must name the same cadence, and neither carries an install-time
// marker.
func TestReminderTimerAssetCarriesTheFiveMinuteTick(t *testing.T) {
	t.Parallel()
	timer, err := readAsset("systemd/" + reminderTimerUnit)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"OnCalendar=*:0/5",
		"Persistent=true",
		"Unit=pfm-reminder.service",
		"WantedBy=timers.target",
	} {
		if !strings.Contains(string(timer), want) {
			t.Errorf("reminder timer asset is missing %q:\n%s", want, timer)
		}
	}
	if strings.Contains(string(timer), "__PFM_") {
		t.Errorf("reminder timer asset carries an install-time marker:\n%s", timer)
	}
}

func TestReminderServiceAssetFiresAndStagesWithoutMarkers(t *testing.T) {
	t.Parallel()
	service, err := readAsset("systemd/" + reminderServiceUnit)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Type=oneshot",
		"ExecStart=%h/.local/bin/pfm internal reminder-fire",
	} {
		if !strings.Contains(string(service), want) {
			t.Errorf("reminder service asset is missing %q:\n%s", want, service)
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
	staged := filepath.Join(home, ".local", "share", "pfm", "install", "systemd", reminderServiceUnit)
	content, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("read staged reminder service: %v", err)
	}
	if strings.Contains(string(content), "__PFM_") {
		t.Fatalf("staged reminder service keeps an unrendered marker:\n%s", content)
	}
	if !strings.Contains(string(content), "Environment=PATH="+servicePath(home)) {
		t.Fatalf("staged reminder service does not carry the service PATH:\n%s", content)
	}
}

func TestWireReminderLaunchAgentWritesTheFiveMinuteAgent(t *testing.T) {
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
	if err := installer.wireReminderLaunchAgent(context.Background()); err != nil {
		t.Fatalf("wireReminderLaunchAgent: %v", err)
	}
	path := filepath.Join(home, "Library", "LaunchAgents", reminderLaunchdLabel+".plist")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("reminder launch agent was not written: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("reminder launch agent is a symlink; launchd will never load it")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plist := string(content)
	if strings.Contains(plist, "__PFM_") {
		t.Errorf("reminder launch agent keeps an unrendered marker:\n%s", plist)
	}
	wantArgv := "<string>" + home + "/.local/bin/pfm</string>\n" +
		"\t\t<string>internal</string>\n" +
		"\t\t<string>reminder-fire</string>"
	if !strings.Contains(plist, wantArgv) {
		t.Errorf("reminder launch agent argv is not this home's pfm internal reminder-fire:\n%s", plist)
	}
	if !strings.Contains(plist, "<key>StartInterval</key>\n\t<integer>300</integer>") {
		t.Errorf("reminder launch agent does not tick every 300 s:\n%s", plist)
	}

	// A second wire is a no-op, and unwire removes the agent.
	if err := installer.wireReminderLaunchAgent(context.Background()); err != nil {
		t.Fatalf("second wireReminderLaunchAgent: %v", err)
	}
	if err := installer.unwireReminderLaunchAgent(context.Background()); err != nil {
		t.Fatalf("unwireReminderLaunchAgent: %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("unwire left the reminder launch agent at %s: %v", path, err)
	}
	if err := installer.unwireReminderLaunchAgent(context.Background()); err != nil {
		t.Fatalf("unwire of an absent reminder launch agent: %v", err)
	}
}

func TestReminderUnitsAreManagedAndEnabled(t *testing.T) {
	t.Parallel()
	for _, unit := range []string{reminderServiceUnit, reminderTimerUnit} {
		if !slices.Contains(unitNames, unit) {
			t.Errorf("unitNames = %v, want it to contain %q", unitNames, unit)
		}
	}
	found := false
	for _, enablement := range unitEnablements {
		if enablement.unit == reminderTimerUnit && enablement.wants == "timers.target.wants" {
			found = true
		}
	}
	if !found {
		t.Errorf("unitEnablements = %v, want %q under timers.target.wants", unitEnablements, reminderTimerUnit)
	}
}

// Install, uninstall and reinstall leave the reminder pair exactly as name-sync's
// is left: linked and enabled, gone, then linked and enabled again.
func TestReminderUnitsSurviveInstallUninstallReinstall(t *testing.T) {
	t.Parallel()
	if schedulerIsLaunchd {
		t.Skip("systemd units are not installed on a launchd host")
	}
	home := t.TempDir()
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	managed := filepath.Join(home, ".local", "share", "pfm", "install")
	serviceLink := filepath.Join(unitDir, reminderServiceUnit)
	timerLink := filepath.Join(unitDir, reminderTimerUnit)
	enableLink := filepath.Join(unitDir, "timers.target.wants", reminderTimerUnit)
	links := map[string]string{
		serviceLink: filepath.Join(managed, "systemd", reminderServiceUnit),
		timerLink:   filepath.Join(managed, "systemd", reminderTimerUnit),
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

func TestReminderScheduleDrift(t *testing.T) {
	scenarios := []string{"no drift", "drifted unit", "nothing installed", "armed none", "unreadable unit", "no home"}
	if !schedulerIsLaunchd {
		// Only systemd units are links into the managed root.
		scenarios = append(scenarios, "dangling unit link", "missing service")
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, ".config", "systemd", "user", reminderTimerUnit)
			if schedulerIsLaunchd {
				path = filepath.Join(home, "Library", "LaunchAgents", reminderLaunchdLabel+".plist")
			}
			if scenario == "no drift" || scenario == "drifted unit" || scenario == "dangling unit link" ||
				scenario == "missing service" {
				if schedulerIsLaunchd {
					installer := engine{apply: true, stamp: "test", options: Options{
						Home: home, Runner: &fakeRunner{}, Stdout: io.Discard, Sleep: func(time.Duration) {},
					}}
					if err := installer.wireReminderLaunchAgent(context.Background()); err != nil {
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
					path = filepath.Join(home, ".config", "systemd", "user", reminderServiceUnit)
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
				path = filepath.Join(home, ".config", "systemd", "user", reminderServiceUnit)
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
			gotPath, drifted, err := ReminderScheduleDrift(home, scenario == "armed none")
			switch scenario {
			case "no home":
				if err == nil || err.Error() != "reminder schedule drift: no home directory" || drifted ||
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
