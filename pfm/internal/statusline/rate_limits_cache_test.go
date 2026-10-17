package statusline

import (
	"context"
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
		{"partial zero wins", fmt.Sprintf(`,"rate_limits":{"five_hour":{"used_percentage":0,"resets_at":%d}}`, now.Add(time.Hour).Unix()), "fresh", "0%", "78%", "0%"},
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

// mainRates retains the harness-provided main windows in the cache-state cases.
func mainRates(now time.Time) string {
	return fmt.Sprintf(
		`,"rate_limits":{"five_hour":{"used_percentage":11,"resets_at":%d},"seven_day":{"used_percentage":31,"resets_at":%d}}`,
		now.Add(4*time.Hour).Unix(),
		now.Add(6*24*time.Hour).Unix(),
	)
}
