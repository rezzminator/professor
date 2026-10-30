package statusline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

// Missing harness windows must use the same seat's real usage, not turn a
// healthy source into dashes. Values here mirror the live response's shape.
func TestMissingMainWindowsUseUsageCacheWithoutReconfirmingIt(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name, rates, state, five, seven, fable string
	}{
		{"omitted block", "", "fresh", "10%", "78%", "0%"},
		{"null block", `,"rate_limits":null`, "fresh", "10%", "78%", "0%"},
		{"empty block", `,"rate_limits":{}`, "fresh", "10%", "78%", "0%"},
		{"null windows", `,"rate_limits":{"five_hour":null,"seven_day":null}`, "fresh", "10%", "78%", "0%"},
		{"partial zero wins", fmt.Sprintf(`,"rate_limits":{"five_hour":{"used_percentage":0,"resets_at":%d}}`, now.Add(2*time.Hour).Unix()), "fresh", "0%", "78%", "0%"},
		{"explicit empty scope", `,"rate_limits":{"limits":[]}`, "fresh", "10%", "78%", "—"},
		{"missing cache", `,"rate_limits":{"seven_day_fable":{"used_percentage":0}}`, "missing", "—", "—", "0%"},
		{"stale cache", `,"rate_limits":{"seven_day_fable":{"used_percentage":0}}`, "stale", "—", "—", "0%"},
		{"other seat", `,"rate_limits":{"seven_day_fable":{"used_percentage":0}}`, "other", "—", "—", "0%"},
		{"legacy mtime", "", "legacy", "10%", "78%", "0%"},
		{"stale legacy mtime", `,"rate_limits":{"seven_day_fable":{"used_percentage":0}}`, "legacy-stale", "—", "—", "0%"},
		{"backoff", "", "backoff", "ERR", "ERR", "ERR"},
		{"malformed JSON", "", "broken", "ERR", "ERR", "ERR"},
		{"future legacy mtime", "", "legacy-future", "ERR", "ERR", "ERR"},
		{"future stamp recent mtime", "", "future-recent", "ERR", "ERR", "ERR"},
		{"malformed fable reset", mainRates(now), "fable-invalid", "11%", "31%", "ERR"},
		{"future stamp and mtime", "", "future", "ERR", "ERR", "ERR"},
		{"exact one-hour age", "", "boundary", "10%", "78%", "0%"},
		{"past one-hour age", `,"rate_limits":{"seven_day_fable":{"used_percentage":0}}`, "past-boundary", "—", "—", "0%"},
		{"expired main reset", "", "expired", "—", "78%", "0%"},
		{"blank legacy identity", `,"rate_limits":{"seven_day_fable":{"used_percentage":0}}`, "blank", "—", "—", "0%"},
		{"fable missing cache", mainRates(now), "missing", "11%", "31%", "—"},
		{"fable fallback within hour", mainRates(now), "within-hour", "11%", "31%", "0%"},
		{"fable stale", mainRates(now), "fable-stale", "11%", "31%", "—"},
		{"fable broken", mainRates(now), "broken", "11%", "31%", "ERR"},
		{"fable unreadable", mainRates(now), "unreadable", "11%", "31%", "ERR"},
		{"fable backoff", mainRates(now), "backoff", "11%", "31%", "ERR"},
		{"fable expired", mainRates(now), "fable-expired", "11%", "31%", "—"},
		{"fable other seat", mainRates(now), "other", "11%", "31%", "—"},
		{"fable blank identity", mainRates(now), "blank", "11%", "31%", "—"},
		{"invalid main reset", "", "invalid", "ERR", "78%", "0%"},
		{"empty resets keep readings", "", "empty-resets", "0%", "78%", "0%"},
		{"null resets keep readings", "", "null-resets", "0%", "78%", "0%"},
		{"padded resets", "", "padded-resets", "10%", "78%", "0%"},
		{"codex foreign unreadable cache", mainRates(now), "codex-unreadable", "11%", "31%", "—"},
		{"backoff controls", "", "backoff-controls", "ERR", "ERR", "ERR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := fableRuntime(t.TempDir(), 2, now)
			fetched := now.Add(-5 * time.Minute)
			record := usagehook.CacheRecord{Usage: usagehook.Usage{
				FiveHour: usagehook.Window{
					Utilization: floatPtr(10),
					ResetsAt:    now.Add(time.Hour).Format(time.RFC3339),
				},
				SevenDay: usagehook.Window{
					Utilization: floatPtr(78),
					ResetsAt:    now.Add(6 * 24 * time.Hour).Format(time.RFC3339),
				},
				Limits: []usagehook.ScopedLimit{
					fableScopedLimit(0, now.Add(6*24*time.Hour).Format(time.RFC3339), false),
				},
			}, ConfigDir: runtime.ConfigDir, FetchedAt: &fetched}
			switch tc.state {
			case "stale":
				fetched = now.Add(-2 * time.Hour)
			case "other":
				record.ConfigDir = "other-seat"
			case "legacy", "legacy-stale", "legacy-future":
				record.FetchedAt = nil
			case "boundary":
				fetched = now.Add(-usagehook.StaleHorizon)
			case "past-boundary":
				fetched = now.Add(-usagehook.StaleHorizon - time.Nanosecond)
			case "within-hour":
				fetched = now.Add(-59 * time.Minute)
			case "fable-stale":
				fetched = now.Add(-90 * time.Minute)
			case "future", "future-recent":
				fetched = now.Add(24 * time.Hour)
			case "blank":
				record.ConfigDir = ""
			case "expired":
				record.FiveHour.ResetsAt = now.Format(time.RFC3339)
			case "fable-invalid":
				record.Limits[0].ResetsAt = "not-a-timestamp"
			case "fable-expired":
				record.Limits[0].ResetsAt = now.Add(-time.Hour).Format(time.RFC3339)
			case "empty-resets", "null-resets":
				record.FiveHour.Utilization = floatPtr(0)
				record.FiveHour.ResetsAt = ""
				record.SevenDay.ResetsAt = ""
				record.Limits[0].ResetsAt = ""
			case "padded-resets":
				record.FiveHour.ResetsAt = " " + record.FiveHour.ResetsAt + " \t"
				record.SevenDay.ResetsAt = " " + record.SevenDay.ResetsAt + " \t"
				record.Limits[0].ResetsAt = " " + record.Limits[0].ResetsAt + " \t"
			case "backoff", "backoff-controls":
				record.Backoff = &usagehook.CacheBackoff{
					Message:    "429",
					RetryAfter: now.Add(time.Minute),
					RecordedAt: now,
				}
			case "invalid":
				record.FiveHour.ResetsAt = "not-a-time"
			}
			if tc.state != "missing" {
				writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, record)
			}
			if strings.HasPrefix(tc.state, "legacy") || tc.state == "future" || tc.state == "future-recent" {
				path := usagehook.CachePath(usagehook.UsageCacheDir(runtime.CacheDir, runtime.UID), 2)
				stamp := now.Add(-time.Minute)
				switch tc.state {
				case "legacy-stale":
					stamp = now.Add(-2 * time.Hour)
				case "legacy-future", "future":
					stamp = now.Add(24 * time.Hour)
				}
				if err := os.Chtimes(path, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			}
			if tc.state == "null-resets" {
				path := usagehook.CachePath(usagehook.UsageCacheDir(runtime.CacheDir, runtime.UID), 2)
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					path,
					[]byte(strings.ReplaceAll(string(body), `"resets_at":""`, `"resets_at":null`)),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			if tc.state == "backoff-controls" {
				record.Backoff.Message = "provider \x1b[2J\r failure"
				writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, record)
			}
			if tc.state == "codex-unreadable" {
				runtime.Engine = pfmengine.Codex
			}
			if tc.state == "unreadable" || tc.state == "codex-unreadable" {
				path := usagehook.CachePath(usagehook.UsageCacheDir(runtime.CacheDir, runtime.UID), 2)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Clean(path), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if tc.state == "broken" {
				path := usagehook.CachePath(usagehook.UsageCacheDir(runtime.CacheDir, runtime.UID), 2)
				if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var warnings strings.Builder
			runtime.Warn = &warnings
			raw := []byte(`{"model":{"id":"claude-opus-4"}` + tc.rates + `}`)
			got, err := Render(context.Background(), raw, runtime)
			if err != nil {
				t.Fatal(err)
			}
			plain := stripANSICodes(got)
			if (tc.state == "empty-resets" || tc.state == "null-resets") && strings.Contains(plain, "↻") {
				t.Fatalf("unknown reset must not render countdown: %q", plain)
			}
			if tc.state == "backoff-controls" && strings.ContainsAny(warnings.String(), "\x1b\r") {
				t.Fatalf("unescaped provider controls in diagnostic: %q", warnings.String())
			}
			for label, value := range map[string]string{"5h": tc.five, "7d": tc.seven, "7d-fable": tc.fable} {
				if !strings.Contains(plain, label+"-used:"+value) {
					t.Fatalf("missing %s-used:%s in %q", label, value, plain)
				}
			}
			if (tc.five == "ERR" || tc.fable == "ERR") && !strings.Contains(warnings.String(), "usage cache") {
				t.Fatalf("missing contextual cache diagnostic: %q", warnings.String())
			}
			// Read through the snapshot producer's public reader. Any snapshot that
			// exists must carry only the genuinely reported harness zero, never cached
			// 10/78/0 promoted to this draw's confirmation time.
			usage, _, found, err := usagehook.ReadStatuslineSnapshot(
				runtime.RateLimitDir,
				2,
				runtime.ConfigDir,
				now,
				time.Minute,
			)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.name == "partial zero wins":
				if !found || usage.FiveHour.Utilization == nil || *usage.FiveHour.Utilization != 0 ||
					usage.SevenDay.Utilization != nil ||
					len(usage.Limits) != 0 {
					t.Fatalf("snapshot must contain exactly harness five_hour=0, got found=%v usage=%+v", found, usage)
				}
			case tc.five == "11%":
				if !found || usage.FiveHour.Utilization == nil || *usage.FiveHour.Utilization != 11 ||
					usage.SevenDay.Utilization == nil || *usage.SevenDay.Utilization != 31 || len(usage.Limits) != 0 {
					t.Fatalf("snapshot must contain exactly harness 11/31, got found=%v usage=%+v", found, usage)
				}
			case found:
				t.Fatalf("cache-only draw created a freshly confirmed snapshot: %+v", usage)
			}
		})
	}
}

// mainRates retains the harness-provided main windows in the cache-state cases:
// both sit in a later window than the cache's, so the cache never outranks them.
func mainRates(now time.Time) string {
	return fmt.Sprintf(
		`,"rate_limits":{"five_hour":{"used_percentage":11,"resets_at":%d},"seven_day":{"used_percentage":31,"resets_at":%d}}`,
		now.Add(4*time.Hour).Unix(),
		now.Add(6*24*time.Hour+time.Hour).Unix(),
	)
}

// An idle chat's payload stops at its last API reply while the account keeps
// spending, so a reported window yields to a newer reading of it: another seat's
// snapshot or the usage cache. Newer is the later window, or more used inside
// the same one; the borrowed reading is never recorded as this seat's own.
func TestAFrozenPayloadYieldsToANewerReadingOfItsWindow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	week := now.Add(60 * time.Hour)
	for _, tc := range []struct {
		name, source string
		used         float64
		reset        time.Time
		payloadUsed  int
		payloadReset time.Time
		want         string
	}{
		{"sibling seat newer", "sibling", 89, week, 61, week, "89%"},
		{"usage cache newer", "cache", 89, week, 61, week, "89%"},
		{"sibling seat behind", "sibling", 40, week, 61, week, "61%"},
		{"usage cache behind", "cache", 40, week, 61, week, "61%"},
		{"payload in a later window", "cache", 89, week, 3, week.Add(7 * 24 * time.Hour), "3%"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := fableRuntime(t.TempDir(), 2, now)
			switch tc.source {
			case "sibling":
				body, err := json.Marshal(map[string]any{
					"acct": 2, "config_dir": runtime.ConfigDir, "ts": now.Add(-10 * time.Minute).Unix(),
					"windows": map[string]any{
						"seven_day": map[string]any{"used_percentage": tc.used, "resets_at": tc.reset.Unix()},
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(runtime.RateLimitDir, 0o700); err != nil {
					t.Fatal(err)
				}
				live := filepath.Join(runtime.RateLimitDir, "acct-2.live.json")
				if err := os.WriteFile(live, body, 0o600); err != nil {
					t.Fatal(err)
				}
			case "cache":
				fetched := now.Add(-5 * time.Minute)
				writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
					Usage: usagehook.Usage{SevenDay: usagehook.Window{
						Utilization: floatPtr(tc.used),
						ResetsAt:    tc.reset.Format(time.RFC3339),
					}},
					ConfigDir: runtime.ConfigDir,
					FetchedAt: &fetched,
				})
			}
			raw := fmt.Sprintf(
				`{"model":{"id":"claude-opus-4"},"session_id":"frozen","rate_limits":{`+
					`"five_hour":{"used_percentage":0,"resets_at":%d},`+
					`"seven_day":{"used_percentage":%d,"resets_at":%d}}}`,
				now.Add(time.Hour).Unix(), tc.payloadUsed, tc.payloadReset.Unix(),
			)
			got, err := Render(context.Background(), []byte(raw), runtime)
			if err != nil {
				t.Fatal(err)
			}
			if plain := stripANSICodes(got); !strings.Contains(plain, "7d-used:"+tc.want) {
				t.Fatalf("want 7d-used:%s in %q", tc.want, plain)
			}
			body, err := os.ReadFile(filepath.Join(runtime.RateLimitDir, "acct-2.frozen.json"))
			if err != nil {
				t.Fatal(err)
			}
			var own struct {
				Windows map[string]rateWindow `json:"windows"`
			}
			if err := json.Unmarshal(body, &own); err != nil {
				t.Fatal(err)
			}
			if seven, ok := own.Windows["seven_day"]; ok && seven.UsedPercentage != float64(tc.payloadUsed) {
				t.Fatalf("this seat's snapshot recorded a borrowed 7d reading: %+v", seven)
			}
		})
	}
}

// A redraw is no provider confirmation: an idle chat redraws every few seconds
// with the payload of its last API reply, so its snapshot keeps the time that
// reading last changed. Otherwise the frozen reading answers the usage door
// inside its TTL forever and the endpoint is never asked again. A reading of
// this seat's own that the display replaced with a sibling's stays recorded, so
// the borrowed one coming and going is no change either.
func TestARedrawOfAnUnchangedPayloadKeepsItsConfirmationTime(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	week := start.Add(60 * time.Hour)
	frozen := fmt.Sprintf(
		`{"model":{"id":"claude-opus-4"},"session_id":"frozen","rate_limits":{`+
			`"five_hour":{"used_percentage":20,"resets_at":%d},`+
			`"seven_day":{"used_percentage":61,"resets_at":%d}}}`,
		start.Add(4*time.Hour).Unix(), week.Unix(),
	)
	root := t.TempDir()
	draw := func(at time.Time, raw string) int64 {
		t.Helper()
		runtime := fableRuntime(root, 2, at)
		if _, err := Render(context.Background(), []byte(raw), runtime); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(runtime.RateLimitDir, "acct-2.frozen.json"))
		if err != nil {
			t.Fatal(err)
		}
		var own struct {
			TS      int64                 `json:"ts"`
			Windows map[string]rateWindow `json:"windows"`
		}
		if err := json.Unmarshal(body, &own); err != nil {
			t.Fatal(err)
		}
		if seven := own.Windows["seven_day"]; seven.UsedPercentage != 61 {
			t.Fatalf("at %s the snapshot's own 7d reading is %+v, want this seat's 61%%", at, seven)
		}
		return own.TS
	}
	if got := draw(start, frozen); got != start.Unix() {
		t.Fatalf("first draw ts=%d, want %d", got, start.Unix())
	}
	later := start.Add(10 * time.Minute)
	if got := draw(later, frozen); got != start.Unix() {
		t.Fatalf("an unchanged redraw re-stamped ts=%d, want the reading's %d", got, start.Unix())
	}
	runtime := fableRuntime(root, 2, later)
	if _, _, found, err := usagehook.ReadStatuslineSnapshot(
		runtime.RateLimitDir, 2, runtime.ConfigDir, later, 3*time.Minute,
	); err != nil || found {
		t.Fatalf("found=%v err=%v: a frozen reading answered the usage door inside its TTL", found, err)
	}
	sibling, err := json.Marshal(map[string]any{
		"acct": 2, "config_dir": runtime.ConfigDir, "ts": later.Add(-time.Minute).Unix(),
		"windows": map[string]any{
			"seven_day": map[string]any{"used_percentage": 89, "resets_at": week.Unix()},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime.RateLimitDir, "acct-2.live.json"), sibling, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := draw(later.Add(time.Minute), frozen); got != start.Unix() {
		t.Fatalf("adopting a sibling's 7d re-stamped ts=%d, want %d", got, start.Unix())
	}
	replied := strings.Replace(frozen, `"used_percentage":20`, `"used_percentage":25`, 1)
	if at := later.Add(2 * time.Minute); draw(at, replied) != at.Unix() {
		t.Fatalf("a changed reading kept its old confirmation time")
	}
}
