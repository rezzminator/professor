package usagehook

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
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

// Evaluate renders each cached window state: a passed reset says so instead of a stale number, a live one keeps
// its reading, an unparsable reset counts as live, and a warned seat whose usage fell gets the recovery banner.
func TestEvaluateRendersEachCachedWindowState(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	at := func(offset time.Duration) string { return now.Add(offset).Format(time.RFC3339) }
	for _, row := range []struct {
		name   string
		five   Window
		seven  float64
		warned bool
		want   string
	}{
		{"live 7d warns when 5h expired", Window{Utilization: usageFloatPtr(99), ResetsAt: at(-time.Minute)}, 91, false, "⚠ usage limit approaching — account 5: 5h — (reset passed) · 7d 91% (resets Tue 09:30). Plan around it: wrap up soon or /reload to another account before the cap hits.\n | <nil>"},
		{"empty reset is live", Window{Utilization: usageFloatPtr(97)}, 12, false, "🔴 USAGE LIMIT IMMINENT — account 5: 5h 97% (resets ?) · 7d 12% (resets Tue 09:30). The 5-hour window is nearly exhausted: finish the in-flight step, then /reload to another account (or pause until the reset).\n | <nil>"},
		{"garbled reset is live", Window{Utilization: usageFloatPtr(97), ResetsAt: "not-a-timestamp"}, 12, false, "🔴 USAGE LIMIT IMMINENT — account 5: 5h 97% (resets ?) · 7d 12% (resets Tue 09:30). The 5-hour window is nearly exhausted: finish the in-flight step, then /reload to another account (or pause until the reset).\n | <nil>"},
		{"impossible reset is live", Window{Utilization: usageFloatPtr(97), ResetsAt: "2026-13-45T99:99:99Z"}, 12, false, "🔴 USAGE LIMIT IMMINENT — account 5: 5h 97% (resets ?) · 7d 12% (resets Tue 09:30). The 5-hour window is nearly exhausted: finish the in-flight step, then /reload to another account (or pause until the reset).\n | <nil>"},
		{"passed reset stays silent", Window{Utilization: usageFloatPtr(98), ResetsAt: at(-time.Minute)}, 12, false, " | <nil>"},
		{"live reset warns", Window{Utilization: usageFloatPtr(98), ResetsAt: at(time.Hour)}, 12, false, "🔴 USAGE LIMIT IMMINENT — account 5: 5h 98% (resets 10:30) · 7d 12% (resets Tue 09:30). The 5-hour window is nearly exhausted: finish the in-flight step, then /reload to another account (or pause until the reset).\n | <nil>"},
		{"warn line says reset passed", Window{Utilization: usageFloatPtr(97), ResetsAt: at(-90 * time.Minute)}, 88, false, "⚠ usage limit approaching — account 5: 5h — (reset passed) · 7d 88% (resets Tue 09:30). Plan around it: wrap up soon or /reload to another account before the cap hits.\n | <nil>"},
		{"recovery says reset passed", Window{Utilization: usageFloatPtr(97), ResetsAt: at(-90 * time.Minute)}, 12, true, "✅ usage recovered — account 5 is back to 5h — (reset passed · awaiting refetch) · 7d 12%. Any earlier limit warning in this conversation is STALE — ignore it and work normally.\n | <nil>"},
		{"recovery keeps a live reading", Window{Utilization: usageFloatPtr(5), ResetsAt: at(2 * time.Hour)}, 12, true, "✅ usage recovered — account 5 is back to 5h 5% · 7d 12%. Any earlier limit warning in this conversation is STALE — ignore it and work normally.\n | <nil>"},
	} {
		t.Run(row.name, func(t *testing.T) {
			root, configDir, cacheDir := adversarialSeat(t, 5)
			if row.warned {
				if err := atomicfile.Write(filepath.Join(cacheDir, "warned-5"), []byte(configDir), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := WriteCacheRecord(CachePath(cacheDir, 5), CacheRecord{
				Usage: Usage{
					FiveHour: row.five,
					SevenDay: Window{Utilization: usageFloatPtr(row.seven), ResetsAt: at(96 * time.Hour)},
				},
				ConfigDir: configDir,
				FetchedAt: &now,
			}); err != nil {
				t.Fatal(err)
			}
			message, err := Evaluate(context.Background(), Options{
				Now: func() time.Time { return now }, Home: root, ConfigDir: configDir,
				AccountDirs: map[string]int{configDir: 5}, CacheDir: cacheDir,
				Warn: 80, Critical: 95, TTL: 24 * time.Hour, Log: io.Discard,
			})
			if got := strings.ReplaceAll(fmt.Sprint(message, " | ", err), root, "{root}"); got != row.want {
				t.Errorf("message | err = %q, want %q", got, row.want)
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

// The statusline reads synchronously: cache artifacts must be bounded regular
// files, never links or special files supplied through the shared temp path.
func TestReadCacheRecordRejectsUnsafeArtifacts(t *testing.T) {
	for _, shape := range []string{"leaf symlink", "relative leaf symlink", "directory symlink", "oversized", "fifo"} {
		t.Run(shape, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "cache")
			path := CachePath(dir, 2)
			if err := WriteCacheRecord(path, CacheRecord{ConfigDir: "fixture"}); err != nil {
				t.Fatal(err)
			}
			switch shape {
			case "leaf symlink", "relative leaf symlink":
				target := filepath.Join(dir, "real.json")
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if shape == "relative leaf symlink" {
					target = filepath.Base(target)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "directory symlink":
				target := filepath.Join(root, "real")
				if err := os.Rename(dir, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, dir); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				body := []byte(`{"config_dir":"fixture"}` + strings.Repeat(" ", 1<<20))
				if err := os.WriteFile(path, body, 0o600); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			go func() { _, err := ReadCacheRecord(path); done <- err }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("unsafe %s accepted as a normal cache", shape)
				}
			case <-time.After(time.Second):
				// Release an unfixed blocking FIFO reader before failing the case.
				writer, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
				if err != nil {
					t.Fatal(fmt.Errorf("release blocked reader: %w", err))
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				<-done
				t.Fatalf("unsafe %s blocked the synchronous cache reader", shape)
			}
		})
	}
}

// An atomic publisher may replace the pathname after the reader opens it.
// Legacy age must still describe that descriptor's payload, not the new file.
func TestReadCacheRecordKeepsLegacyMetadataWithItsBytes(t *testing.T) {
	path := CachePath(t.TempDir(), 2)
	if err := WriteCacheRecord(path, CacheRecord{ConfigDir: "old"}); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := WriteCacheRecord(path, CacheRecord{ConfigDir: "new"}); err != nil {
		t.Fatal(err)
	}
	record, modified, err := readCacheRecordFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if record.ConfigDir != "old" || !modified.Equal(stamp) {
		t.Fatalf(
			"legacy bytes and age split across replacement: record=%+v modified=%s want=%s",
			record,
			modified,
			stamp,
		)
	}
}

// The error-preserving selector keeps real readings with unknown resets, an
// empty one quietly and a malformed one beside its decode error, while the
// legacy selector continues to require a future reset.
func TestFableWindowAtKeepsUnknownResetWithoutChangingLegacySelector(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name, reset, wantErr string
	}{
		{name: "empty", reset: ""},
		{
			name: "malformed", reset: "soon",
			wantErr: `decode cached Fable reset "soon": parsing time "soon" as "2006-01-02T15:04:05Z07:00": ` +
				`cannot parse "soon" as "2006"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			percent := 0.0
			limit := ScopedLimit{Kind: "weekly_scoped", Percent: &percent, ResetsAt: test.reset}
			limit.Scope.Model.DisplayName = "Fable"
			usage := Usage{Limits: []ScopedLimit{limit}}
			window, ok, err := usage.FableWindowAt(now)
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if gotErr != test.wantErr || !ok || window.Utilization == nil || *window.Utilization != 0 ||
				window.ResetsAt != test.reset {
				t.Fatalf("unknown-reset selector: window=%+v ok=%v err=%q, want the reading at %q beside %q",
					window, ok, gotErr, test.reset, test.wantErr)
			}
			if _, legacy := usage.fableWindow(now); legacy {
				t.Fatal("legacy selector unexpectedly accepted unknown reset")
			}
		})
	}
}
