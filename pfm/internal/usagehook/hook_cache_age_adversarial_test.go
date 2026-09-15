package usagehook

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func adversarialSeat(t *testing.T, account int) (root, configDir, cacheDir string) {
	t.Helper()
	root = t.TempDir()
	configDir = filepath.Join(root, ".cc", itoa(account))
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"claudeAiOauth":{"accessToken":"fixture-token"}}`)
	if err := os.WriteFile(filepath.Join(configDir, ".credentials.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	return root, configDir, filepath.Join(root, "cache")
}

// a record written by a build that predates fetched_at must age by the
// FILE's mtime — the documented fallback. An hours-old legacy file must not
// warn; a just-written one still must.
func TestEvaluateFallsBackToMtimeWhenFetchedAtIsAbsent(t *testing.T) {
	for _, testcase := range []struct {
		name        string
		mtimeAge    time.Duration
		ttl         time.Duration
		wantWarning bool
		wantHits    int
	}{
		{name: "legacy file two hours old", mtimeAge: 2 * time.Hour, ttl: 10 * time.Minute, wantHits: 1},
		{name: "legacy file just written", mtimeAge: time.Minute, ttl: 24 * time.Hour, wantWarning: true},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			root, configDir, cacheDir := adversarialSeat(t, 2)
			now := time.Now().Truncate(time.Second)
			cachePath := CachePath(cacheDir, 2)
			if err := WriteCacheRecord(cachePath, CacheRecord{
				Usage: Usage{
					FiveHour: Window{
						Utilization: usageFloatPtr(97),
						ResetsAt:    now.Add(3 * time.Hour).Format(time.RFC3339),
					},
					SevenDay: Window{
						Utilization: usageFloatPtr(97),
						ResetsAt:    now.Add(5 * 24 * time.Hour).Format(time.RFC3339),
					},
				},
				ConfigDir: configDir,
			}); err != nil {
				t.Fatal(err)
			}
			stamp := now.Add(-testcase.mtimeAge)
			if err := os.Chtimes(cachePath, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				hits++
				writer.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			message, err := Evaluate(context.Background(), Options{
				Now: func() time.Time { return now }, Home: root, ConfigDir: configDir,
				AccountDirs: map[string]int{configDir: 2}, CacheDir: cacheDir,
				Warn: 80, Critical: 95, TTL: testcase.ttl,
				Client: server.Client(), Endpoint: server.URL, Log: io.Discard,
			})
			if err != nil {
				t.Fatal(err)
			}
			if warned := message != ""; warned != testcase.wantWarning {
				t.Fatalf("warned=%v want %v: message=%q", warned, testcase.wantWarning, message)
			}
			if hits != testcase.wantHits {
				t.Fatalf("provider requests=%d, want %d", hits, testcase.wantHits)
			}
		})
	}
}

// a record stamped in the FUTURE has an unverifiable age. The stats
// sampler already refuses one (cacheFresh: `!confirmedAt.After(now)`, plus the
// "future confirmation timestamp" guard in LimitsSampler.refresh); after the
// hook started aging by fetched_at instead of mtime, the same corrupt or
// clock-skewed stamp makes the hook's cache permanently fresh: it never refreshes and
// keeps warning from whatever payload carries the stamp.
func TestEvaluateRejectsAFutureFetchedAt(t *testing.T) {
	root, configDir, cacheDir := adversarialSeat(t, 4)
	now := time.Now().Truncate(time.Second)
	future := now.Add(100 * 24 * time.Hour)
	cachePath := CachePath(cacheDir, 4)
	if err := WriteCacheRecord(cachePath, CacheRecord{
		Usage: Usage{
			FiveHour: Window{
				Utilization: usageFloatPtr(99),
				ResetsAt:    now.Add(200 * 24 * time.Hour).Format(time.RFC3339),
			},
			SevenDay: Window{
				Utilization: usageFloatPtr(99),
				ResetsAt:    now.Add(200 * 24 * time.Hour).Format(time.RFC3339),
			},
		},
		ConfigDir: configDir, FetchedAt: &future,
	}); err != nil {
		t.Fatal(err)
	}
	// The file itself is two hours old: pre-2026-09-11 the hook aged by this
	// mtime and refreshed. Only the record's forward-dated stamp can suppress
	// the request now.
	stamp := now.Add(-2 * time.Hour)
	if err := os.Chtimes(cachePath, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits++
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	message, err := Evaluate(context.Background(), Options{
		Now: func() time.Time { return now }, Home: root, ConfigDir: configDir,
		AccountDirs: map[string]int{configDir: 4}, CacheDir: cacheDir,
		Warn: 80, Critical: 95, TTL: 3 * time.Minute,
		Client: server.Client(), Endpoint: server.URL, Log: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("future-stamped record suppressed the refresh: provider requests=%d, want 1", hits)
	}
	if message != "" {
		t.Fatalf("future-stamped record warned as fresh: %q", message)
	}
}

// 5h expired while 7d is live must still warn, and must warn ON the 7d
// number — the expired window contributes only its fallback.
func TestEvaluateStillWarnsOnTheLiveSevenDayWhenFiveHourExpired(t *testing.T) {
	root, configDir, cacheDir := adversarialSeat(t, 5)
	now := time.Now().Truncate(time.Second)
	if err := WriteCacheRecord(CachePath(cacheDir, 5), CacheRecord{
		Usage: Usage{
			FiveHour: Window{Utilization: usageFloatPtr(99), ResetsAt: now.Add(-time.Minute).Format(time.RFC3339)},
			SevenDay: Window{
				Utilization: usageFloatPtr(91),
				ResetsAt:    now.Add(4 * 24 * time.Hour).Format(time.RFC3339),
			},
		},
		ConfigDir: configDir, FetchedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	message, err := Evaluate(context.Background(), Options{
		Now: func() time.Time { return now }, Home: root, ConfigDir: configDir,
		AccountDirs: map[string]int{configDir: 5}, CacheDir: cacheDir,
		Warn: 80, Critical: 95, TTL: 24 * time.Hour, Log: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, "⚠ usage limit approaching") || !strings.Contains(message, "7d 91%") {
		t.Fatalf("live 7d lost its warning when 5h expired: %q", message)
	}
	if strings.Contains(message, "5h 99%") {
		t.Fatalf("expired 5h reported its stale reading: %q", message)
	}
}

// an empty or unparsable resets_at is UNKNOWN, not expired — the reading
// must still count toward the warning.
func TestEvaluateTreatsUnparsableResetsAtAsUnknownNotExpired(t *testing.T) {
	for _, resets := range []string{"", "not-a-timestamp", "2026-13-45T99:99:99Z"} {
		t.Run("resets_at="+resets, func(t *testing.T) {
			root, configDir, cacheDir := adversarialSeat(t, 6)
			now := time.Now().Truncate(time.Second)
			if err := WriteCacheRecord(CachePath(cacheDir, 6), CacheRecord{
				Usage: Usage{
					FiveHour: Window{Utilization: usageFloatPtr(97), ResetsAt: resets},
					SevenDay: Window{
						Utilization: usageFloatPtr(12),
						ResetsAt:    now.Add(4 * 24 * time.Hour).Format(time.RFC3339),
					},
				},
				ConfigDir: configDir, FetchedAt: &now,
			}); err != nil {
				t.Fatal(err)
			}
			message, err := Evaluate(context.Background(), Options{
				Now: func() time.Time { return now }, Home: root, ConfigDir: configDir,
				AccountDirs: map[string]int{configDir: 6}, CacheDir: cacheDir,
				Warn: 80, Critical: 95, TTL: 24 * time.Hour, Log: io.Discard,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(message, "USAGE LIMIT IMMINENT") {
				t.Fatalf("unknown reset was treated as expired and silenced the warning: %q", message)
			}
		})
	}
}

// CachedFableWindow's hour, now measured by cacheAge, must keep its
// mtime fallback for legacy records and must still refuse a stale one.
func TestCachedFableWindowAgesLegacyRecordsByMtime(t *testing.T) {
	for _, testcase := range []struct {
		name     string
		mtimeAge time.Duration
		wantOK   bool
	}{
		{name: "fresh legacy record", mtimeAge: time.Minute, wantOK: true},
		{name: "two-hour-old legacy record", mtimeAge: 2 * time.Hour},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			base := t.TempDir()
			configDir := filepath.Join(base, ".cc", "7")
			now := time.Now().Truncate(time.Second)
			percent := 44.0
			scoped := ScopedLimit{
				Kind: "weekly_scoped", Percent: &percent,
				ResetsAt: now.Add(72 * time.Hour).Format(time.RFC3339), IsActive: true,
			}
			scoped.Scope.Model.DisplayName = "Fable"
			path := CachePath(UsageCacheDir(base, os.Getuid()), 7)
			if err := WriteCacheRecord(path, CacheRecord{
				Usage: Usage{Limits: []ScopedLimit{scoped}}, ConfigDir: configDir,
			}); err != nil {
				t.Fatal(err)
			}
			stamp := now.Add(-testcase.mtimeAge)
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			window, ok := CachedFableWindow(base, os.Getuid(), 7, configDir, now)
			if ok != testcase.wantOK {
				t.Fatalf("CachedFableWindow ok=%v want %v (window=%#v)", ok, testcase.wantOK, window)
			}
			if ok && (window.Utilization == nil || *window.Utilization != 44) {
				t.Fatalf("fable window=%#v", window)
			}
		})
	}
}

// corrupt store files. A truncated, empty, or half-written record must
// send the hook to a refresh, never to a warning built out of garbage.
func TestCorruptCacheRecordsNeverWarn(t *testing.T) {
	for _, testcase := range []struct{ name, body string }{
		{name: "empty file", body: ""},
		{name: "half-written json", body: `{"usage":{"five_hour":{"utilization":99,`},
		{name: "nul bytes", body: "\x00\x00\x00"},
		{name: "wrong shape", body: `[1,2,3]`},
		{name: "fetched_at not a timestamp", body: `{"config_dir":"x","fetched_at":"yesterday"}`},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			root, configDir, cacheDir := adversarialSeat(t, 9)
			now := time.Now().Truncate(time.Second)
			cachePath := CachePath(cacheDir, 9)
			if err := os.MkdirAll(cacheDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cachePath, []byte(testcase.body), 0o600); err != nil {
				t.Fatal(err)
			}
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				hits++
				writer.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			message, err := Evaluate(context.Background(), Options{
				Now: func() time.Time { return now }, Home: root, ConfigDir: configDir,
				AccountDirs: map[string]int{configDir: 9}, CacheDir: cacheDir,
				Warn: 80, Critical: 95, TTL: 3 * time.Minute,
				Client: server.Client(), Endpoint: server.URL, Log: io.Discard,
			})
			if err != nil {
				t.Logf("corrupt record surfaced an error to the caller (fail-open by contract): %v", err)
			}
			if message != "" {
				t.Fatalf("corrupt record produced hook text: %q", message)
			}
			if hits != 1 {
				t.Fatalf("corrupt record did not trigger a refresh: hits=%d", hits)
			}
		})
	}
}
