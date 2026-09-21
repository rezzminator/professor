package installer

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// One value, two schedulers. A host that lowers nameSync.interval and later
// switches managers must not discover a second, older poll waiting for it.
func TestNameSyncIntervalRendersIntoBothSchedulers(t *testing.T) {
	options := Options{NameSyncInterval: 3 * time.Minute}

	plist, err := readAsset(launchdAsset)
	if err != nil {
		t.Fatal(err)
	}
	renderedPlist, err := renderNameSyncLaunchAgent(plist, options)
	if err != nil {
		t.Fatalf("render launch agent: %v", err)
	}
	if !strings.Contains(string(renderedPlist), "<integer>180</integer>") {
		t.Fatalf("launchd StartInterval did not take the configured interval:\n%s", renderedPlist)
	}
	if strings.Contains(string(renderedPlist), launchdIntervalMarker) {
		t.Fatalf("launchd plist still carries the unrendered marker:\n%s", renderedPlist)
	}

	timer, err := readAsset("systemd/" + nameSyncTimerUnit)
	if err != nil {
		t.Fatal(err)
	}
	renderedTimer, err := renderNameSyncTimerAsset(timer, options)
	if err != nil {
		t.Fatalf("render systemd timer: %v", err)
	}
	if !strings.Contains(string(renderedTimer), "OnUnitInactiveSec=3m0s") {
		t.Fatalf("systemd OnUnitInactiveSec did not take the configured interval:\n%s", renderedTimer)
	}
	if strings.Contains(string(renderedTimer), systemdIntervalMarker) {
		t.Fatalf("systemd timer still carries the unrendered marker:\n%s", renderedTimer)
	}
}

func TestNameSyncSchedulersInvokeApply(t *testing.T) {
	home := t.TempDir()
	service, err := readAsset("systemd/pfm-name-sync.service")
	if err != nil {
		t.Fatal(err)
	}
	renderedService, err := renderServicePath(service, home)
	if err != nil {
		t.Fatalf("render systemd service: %v", err)
	}
	if !strings.Contains(string(renderedService), "ExecStart=%h/.local/bin/pfm name-sync --apply") {
		t.Errorf("systemd name-sync argv does not apply changes:\n%s", renderedService)
	}

	plist, err := readAsset(launchdAsset)
	if err != nil {
		t.Fatal(err)
	}
	renderedPlist, err := renderNameSyncLaunchAgent(
		[]byte(strings.ReplaceAll(string(plist), "__PFM_HOME__", home)),
		Options{},
	)
	if err == nil {
		renderedPlist, err = renderServicePath(renderedPlist, home)
	}
	if err != nil {
		t.Fatalf("render launch agent: %v", err)
	}
	wantLaunchdArgv := "<string>name-sync</string>\n\t\t<string>--apply</string>"
	if !strings.Contains(string(renderedPlist), wantLaunchdArgv) {
		t.Errorf("launchd name-sync argv does not apply changes:\n%s", renderedPlist)
	}
}

// A caller with no machine config (a direct Options literal) gets the poll the
// fleet shipped with, never a zero that would make systemd refuse the unit.
func TestNameSyncIntervalFallsBackToTheShippedDefault(t *testing.T) {
	for _, interval := range []time.Duration{0, -time.Hour, time.Second} {
		options := Options{NameSyncInterval: interval}
		if got := nameSyncInterval(options); got != pfmconfig.DefaultNameSyncInterval {
			t.Fatalf("nameSyncInterval(%s) = %s, want %s", interval, got, pfmconfig.DefaultNameSyncInterval)
		}
		timer, err := readAsset("systemd/" + nameSyncTimerUnit)
		if err != nil {
			t.Fatal(err)
		}
		rendered, err := renderNameSyncTimerAsset(timer, options)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(rendered), "OnUnitInactiveSec=15m0s") {
			t.Fatalf("interval %s rendered a timer without the default poll:\n%s", interval, rendered)
		}
	}
}

// The timer the renderer names and the unit the installer links are one unit.
func TestNameSyncTimerUnitIsAManagedUnit(t *testing.T) {
	if !slices.Contains(unitNames, nameSyncTimerUnit) {
		t.Fatalf("unitNames = %v, want it to contain %q", unitNames, nameSyncTimerUnit)
	}
}

func TestNameSyncScheduleSummaryReportsBothSchedulers(t *testing.T) {
	summary := nameSyncScheduleSummary(Options{NameSyncInterval: 90 * time.Second})
	for _, want := range []string{"1m30s", "StartInterval=90", "OnUnitInactiveSec=1m30s"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q missing %q", summary, want)
		}
	}
}

// The staged unit is the one systemd reads (wireUnits links it), so the marker
// must be gone by the time the file lands in the managed root — an unrendered
// OnUnitInactiveSec is a unit systemd refuses to load at all.
func TestApplyStagesTheTimerWithTheConfiguredInterval(t *testing.T) {
	if schedulerIsLaunchd {
		t.Skip("systemd units are not staged on a launchd host")
	}
	home := t.TempDir()
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
		NameSyncInterval: 4 * time.Minute,
	}); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(home, ".local", "share", "pfm", "install", "systemd", nameSyncTimerUnit)
	content, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("read staged timer: %v", err)
	}
	if strings.Contains(string(content), systemdIntervalMarker) {
		t.Fatalf("staged timer carries an unrendered marker systemd cannot parse:\n%s", content)
	}
	if !strings.Contains(string(content), "OnUnitInactiveSec=4m0s") {
		t.Fatalf("staged timer did not take the configured interval:\n%s", content)
	}
}
