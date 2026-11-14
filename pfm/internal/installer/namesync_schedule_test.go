package installer

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

// TestNameSyncIntervalRendersIntoEverySchedulerSurface resolves the poll once
// for both schedulers and the install summary: a configured interval renders
// into launchd's StartInterval and systemd's OnUnitInactiveSec, and a zero,
// negative or sub-minimum one falls back to the shipped default.
func TestNameSyncIntervalRendersIntoEverySchedulerSurface(t *testing.T) {
	t.Parallel()
	const defaultSummary = "name-sync interval 15m0s (launchd StartInterval=900, systemd OnUnitInactiveSec=15m0s)"
	for _, tc := range []struct {
		interval    time.Duration
		want        time.Duration
		wantPlist   string
		wantTimer   string
		wantSummary string
	}{
		{
			3 * time.Minute, 3 * time.Minute, "\t<integer>180</integer>", "OnUnitInactiveSec=3m0s",
			"name-sync interval 3m0s (launchd StartInterval=180, systemd OnUnitInactiveSec=3m0s)",
		},
		{
			90 * time.Second, 90 * time.Second, "\t<integer>90</integer>", "OnUnitInactiveSec=1m30s",
			"name-sync interval 1m30s (launchd StartInterval=90, systemd OnUnitInactiveSec=1m30s)",
		},
		{0, 15 * time.Minute, "\t<integer>900</integer>", "OnUnitInactiveSec=15m0s", defaultSummary},
		{-time.Hour, 15 * time.Minute, "\t<integer>900</integer>", "OnUnitInactiveSec=15m0s", defaultSummary},
		{time.Second, 15 * time.Minute, "\t<integer>900</integer>", "OnUnitInactiveSec=15m0s", defaultSummary},
	} {
		options := Options{NameSyncInterval: tc.interval}
		if got := nameSyncInterval(options); got != tc.want {
			t.Errorf("nameSyncInterval(%s) = %s, want %s", tc.interval, got, tc.want)
		}
		plist, err := readAsset(launchdAsset)
		if err != nil {
			t.Fatal(err)
		}
		renderedPlist, err := renderNameSyncLaunchAgent(plist, options)
		if err != nil {
			t.Fatalf("render launch agent: %v", err)
		}
		if got := outputLines(
			string(renderedPlist),
			"<integer>",
			launchdIntervalMarker,
		); !slices.Equal(
			got,
			[]string{tc.wantPlist},
		) {
			t.Errorf("interval %s: launchd StartInterval lines = %q, want %q", tc.interval, got, tc.wantPlist)
		}
		timer, err := readAsset("systemd/" + nameSyncTimerUnit)
		if err != nil {
			t.Fatal(err)
		}
		renderedTimer, err := renderNameSyncTimerAsset(timer, options)
		if err != nil {
			t.Fatalf("render systemd timer: %v", err)
		}
		if got := outputLines(
			string(renderedTimer),
			"OnUnitInactiveSec",
			systemdIntervalMarker,
		); !slices.Equal(
			got,
			[]string{tc.wantTimer},
		) {
			t.Errorf("interval %s: systemd timer lines = %q, want %q", tc.interval, got, tc.wantTimer)
		}
		if got := nameSyncScheduleSummary(options); got != tc.wantSummary {
			t.Errorf("interval %s: summary = %q, want %q", tc.interval, got, tc.wantSummary)
		}
	}
}

// The timer the renderer names and the unit the installer links are one unit.
func TestNameSyncTimerUnitIsAManagedUnit(t *testing.T) {
	t.Parallel()
	if !slices.Contains(unitNames, nameSyncTimerUnit) {
		t.Fatalf("unitNames = %v, want it to contain %q", unitNames, nameSyncTimerUnit)
	}
}

// The staged unit is the one systemd reads (wireUnits links it), so the marker
// must be gone by the time the file lands in the managed root — an unrendered
// OnUnitInactiveSec is a unit systemd refuses to load at all.
func TestApplyStagesTheTimerWithTheConfiguredInterval(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if _, err := Run(context.Background(), Options{
		scheduler:     schedulerFor(servicemanager.KindSystemd),
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{},
		NameSyncInterval: 4 * time.Minute,
	}); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(home, ".local", "share", "pfm", "install", "systemd", nameSyncTimerUnit)
	content, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("read staged timer: %v", err)
	}
	wantOutputLines(t, string(content), []string{"OnUnitInactiveSec=4m0s"}, "OnUnitInactiveSec", systemdIntervalMarker)
}
