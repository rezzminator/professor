package usagehook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ReadStatuslineSnapshot answers with this seat's newest reading inside
// maxAge: another config directory or another account's file never answers,
// the answer carries its reading's confirmation, and a file claiming the wrong
// account under this account's name is an error rather than silence.
func TestReadStatuslineSnapshotKeepsOnlyThisSeatsNewestReading(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	directory := t.TempDir()
	configDir := filepath.Join(t.TempDir(), ".claude")
	reset := now.Add(2 * time.Hour).Unix()
	write := func(name string, account int, dir string, age time.Duration, used int) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"acct": account, "config_dir": dir, "ts": now.Add(-age).Unix(),
			"five_hour_used": used, "five_hour_resets_at": reset,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("acct-3.older.json", 3, configDir, 20*time.Minute, 30)
	write("acct-3.newer.json", 3, configDir, 30*time.Second, 40)
	write("acct-3.other-seat.json", 3, filepath.Join(t.TempDir(), ".claude"), time.Second, 90)
	write("acct-4.json", 4, configDir, time.Second, 95)

	usage, confirmedAt, found, err := ReadStatuslineSnapshot(directory, 3, configDir, now, time.Hour)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v, want this seat's newest reading", found, err)
	}
	if !confirmedAt.Equal(now.Add(-30*time.Second)) || usage.FiveHour.Utilization == nil ||
		*usage.FiveHour.Utilization != 40 {
		t.Fatalf("answered %v at %s, want 40%% confirmed 30s ago", usage.FiveHour, confirmedAt)
	}
	if _, _, found, err := ReadStatuslineSnapshot(directory, 3, configDir, now, 10*time.Second); err != nil || found {
		t.Fatalf("maxAge 10s: found=%v err=%v, want nothing young enough", found, err)
	}

	write("acct-3.liar.json", 5, configDir, time.Second, 10)
	if _, _, _, err := ReadStatuslineSnapshot(
		directory, 3, configDir, now, time.Hour,
	); err == nil || !strings.Contains(err.Error(), "claims account 5") {
		t.Fatalf("mislabelled snapshot: err=%v, want a claims-account error", err)
	}
}

// A snapshot carrying the `windows` map answers from it: the flat keys stay
// beside it only for an older build's reader, and a reading only the map holds
// (the seven-day Opus window, a fractional percentage) still arrives. A
// snapshot an older build wrote, with no map, reads from its flat keys (the
// test above).
func TestReadStatuslineSnapshotPrefersTheWindowsMap(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	directory := t.TempDir()
	configDir := filepath.Join(t.TempDir(), ".claude")
	fiveReset := now.Add(2 * time.Hour).Unix()
	weekReset := now.Add(5 * 24 * time.Hour).Unix()
	body, err := json.Marshal(map[string]any{
		"acct": 3, "config_dir": configDir, "ts": now.Add(-time.Second).Unix(),
		"five_hour_used": 40, "five_hour_resets_at": fiveReset,
		"windows": map[string]any{
			"five_hour":       map[string]any{"used_percentage": 12.5, "resets_at": fiveReset},
			"seven_day_opus":  map[string]any{"used_percentage": 7, "resets_at": weekReset},
			"seven_day_fable": map[string]any{"used_percentage": 0, "resets_at": weekReset},
			"spend_limit":     map[string]any{"used_percentage": 30, "resets_at": weekReset},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "acct-3.map.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	usage, _, found, err := ReadStatuslineSnapshot(directory, 3, configDir, now, time.Hour)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v, want the map-bearing snapshot", found, err)
	}
	if usage.FiveHour.Utilization == nil || *usage.FiveHour.Utilization != 12.5 {
		t.Fatalf("five_hour=%v, want 12.5 from the windows map, not the flat 40", usage.FiveHour)
	}
	if usage.SevenOpus.Utilization == nil || *usage.SevenOpus.Utilization != 7 {
		t.Fatalf("seven_day_opus=%v, want 7 from the windows map", usage.SevenOpus)
	}
	fable, ok := usage.fableWindow(now)
	if !ok || fable.Utilization == nil || *fable.Utilization != 0 {
		t.Fatalf("fable=%v ok=%v, want the 0%% Fable reading from the windows map", fable, ok)
	}
}

// A confirmation time orders snapshots, never readings: a seat that redraws a
// changed five-hour reading may still hold a seven-day one another seat has
// since passed. The answer is therefore the newest reading per window across
// seats: the later window, or more used inside the same one.
func TestReadStatuslineSnapshotTakesTheNewestReadingPerWindow(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	directory := t.TempDir()
	configDir := filepath.Join(t.TempDir(), ".claude")
	fiveReset := now.Add(2 * time.Hour).Unix()
	weekReset := now.Add(60 * time.Hour).Unix()
	write := func(name string, age time.Duration, five, week float64) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"acct": 3, "config_dir": configDir, "ts": now.Add(-age).Unix(),
			"windows": map[string]any{
				"five_hour": map[string]any{"used_percentage": five, "resets_at": fiveReset},
				"seven_day": map[string]any{"used_percentage": week, "resets_at": weekReset},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("acct-3.frozen.json", time.Second, 20, 61)
	write("acct-3.live.json", 10*time.Minute, 12, 89)

	usage, confirmedAt, found, err := ReadStatuslineSnapshot(directory, 3, configDir, now, time.Hour)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v, want the merged reading", found, err)
	}
	if usage.SevenDay.Utilization == nil || *usage.SevenDay.Utilization != 89 {
		t.Fatalf("seven_day=%v, want the live seat's 89%% over the frozen 61%%", usage.SevenDay)
	}
	if usage.FiveHour.Utilization == nil || *usage.FiveHour.Utilization != 20 {
		t.Fatalf("five_hour=%v, want 20%%, the higher reading of the same window", usage.FiveHour)
	}
	if !confirmedAt.Equal(now.Add(-10 * time.Minute)) {
		t.Fatalf("confirmedAt=%s, want the oldest confirmation among the readings answered", confirmedAt)
	}
}

// NewerReading orders two readings by their window, never by when they were
// stamped: a later reset is a later window, inside one window (a reset within
// the slack) the higher reading wins, and an undated reading loses to a dated one.
func TestNewerReading(t *testing.T) {
	const reset = int64(1_800_000_000)
	for _, tc := range []struct {
		name                 string
		used, thanUsed       float64
		resetsAt, thanResets int64
		want                 bool
	}{
		{"later window at zero", 0, 89, reset + 3600, reset, true},
		{"earlier window however high", 99, 3, reset - 3600, reset, false},
		{"same window higher", 89, 61, reset, reset, true},
		{"same window lower", 61, 89, reset, reset, false},
		{"same window equal", 61, 61, reset, reset, false},
		{"sub-minute jitter is one window", 89, 61, reset - 59, reset, true},
		{"undated loses to dated", 99, 1, 0, reset, false},
		{"dated beats undated", 1, 99, reset, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewerReading(tc.used, tc.resetsAt, tc.thanUsed, tc.thanResets); got != tc.want {
				t.Fatalf("NewerReading(%v,%d,%v,%d)=%v, want %v",
					tc.used, tc.resetsAt, tc.thanUsed, tc.thanResets, got, tc.want)
			}
		})
	}
}

// A merged answer is only as fresh as its oldest reading: a window taken from a
// snapshot confirmed ten minutes ago is not confirmed a second ago because a
// seat whose readings lost redrew since.
func TestReadStatuslineSnapshotConfirmsAMergeAtItsOldestReading(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	directory := t.TempDir()
	configDir := filepath.Join(t.TempDir(), ".claude")
	reset := now.Add(60 * time.Hour).Unix()
	write := func(name string, age time.Duration, week float64) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"acct": 3, "config_dir": configDir, "ts": now.Add(-age).Unix(),
			"windows": map[string]any{"seven_day": map[string]any{"used_percentage": week, "resets_at": reset}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("acct-3.frozen.json", time.Second, 61)
	write("acct-3.live.json", 10*time.Minute, 89)

	usage, confirmedAt, found, err := ReadStatuslineSnapshot(directory, 3, configDir, now, time.Hour)
	if err != nil || !found || usage.SevenDay.Utilization == nil || *usage.SevenDay.Utilization != 89 {
		t.Fatalf("usage=%+v found=%v err=%v, want the 89%% reading", usage.SevenDay, found, err)
	}
	if !confirmedAt.Equal(now.Add(-10 * time.Minute)) {
		t.Fatalf("confirmedAt=%s, want %s, the confirmation of the reading answered",
			confirmedAt, now.Add(-10*time.Minute))
	}
}
