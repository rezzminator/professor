package usagehook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/atomicfile"
)

func TestUsageParsesScopedFableAndDropsUnknownWindows(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "usage-fable.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed Usage
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode usage fixture: %v", err)
	}
	windows := parsed.NamedWindows()
	want := []struct {
		label string
		used  int
	}{{label: "5h", used: 42}, {label: "7d", used: 58}, {label: "7d-fable", used: 24}}
	if len(windows) != len(want) {
		t.Fatalf("NamedWindows()=%#v, want exactly 5h, 7d, and scoped Fable", windows)
	}
	for index, expected := range want {
		if windows[index].Label != expected.label || windows[index].Window.Utilization == nil ||
			int(*windows[index].Window.Utilization) != expected.used {
			t.Fatalf("NamedWindows()[%d]=%#v, want %#v", index, windows[index], expected)
		}
	}
}

func TestNamedWindowsUsesOneCanonicalMappingAndDropsUnknownKeys(t *testing.T) {
	var parsed Usage
	content := []byte(`{
		"five_hour":{"utilization":4,"resets_at":"2030-01-01T10:00:00Z"},
		"seven_day":{"utilization":11,"resets_at":"2026-08-28T00:00:00Z"},
		"seven_day_opus":{"utilization":22,"resets_at":"2026-08-28T00:00:00Z"},
		"limits":[{"kind":"weekly_scoped","percent":23,"resets_at":"2030-01-07T08:00:00Z","scope":{"model":{"display_name":"FABLE"}},"is_active":true}],
		"seven_day_nimbus_quill":{"utilization":33,"resets_at":"2026-08-28T00:00:00Z"}
	}`)
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatal(err)
	}
	windows := parsed.NamedWindows()
	want := []struct {
		key, label string
		known      bool
	}{
		{key: "five_hour", label: "5h", known: true},
		{key: "seven_day", label: "7d", known: true},
		{key: "seven_day_fable", label: "7d-fable", known: true},
	}
	if len(windows) != len(want) {
		t.Fatalf("NamedWindows() = %#v, want %d windows", windows, len(want))
	}
	for index, expected := range want {
		if windows[index].Key != expected.key || windows[index].Label != expected.label ||
			windows[index].Known != expected.known {
			t.Fatalf("NamedWindows()[%d] = %#v, want %#v", index, windows[index], expected)
		}
	}
}

func TestScopedFableRequiresAValidFutureReset(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		kind    string
		reset   string
		present bool
	}{
		{name: "future", kind: "weekly_scoped", reset: now.Add(time.Hour).Format(time.RFC3339), present: true},
		{name: "past", kind: "weekly_scoped", reset: now.Add(-time.Second).Format(time.RFC3339)},
		{name: "invalid", kind: "weekly_scoped", reset: "not-a-time"},
		{name: "wrong kind", kind: "weekly_all", reset: now.Add(time.Hour).Format(time.RFC3339)},
	} {
		t.Run(test.name, func(t *testing.T) {
			percent := 17.0
			limit := ScopedLimit{Kind: test.kind, Percent: &percent, ResetsAt: test.reset, IsActive: true}
			limit.Scope.Model.DisplayName = " FABLE "
			windows := (Usage{Limits: []ScopedLimit{limit}}).NamedWindowsAt(now)
			if got := len(windows) == 1; got != test.present {
				t.Fatalf("NamedWindowsAt()=%#v, present=%v", windows, test.present)
			}
		})
	}
}

func TestLiveKeysetRecordsTopLevelOpusButNoTopLevelFable(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "usage-live-keyset.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		TopLevelKeys []string `json:"top_level_keys"`
	}
	if err := json.Unmarshal(body, &recorded); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(recorded.TopLevelKeys, "limits") ||
		!slices.Contains(recorded.TopLevelKeys, "seven_day_opus") ||
		slices.Contains(recorded.TopLevelKeys, "seven_day_fable") {
		t.Fatalf("recorded live key set=%v", recorded.TopLevelKeys)
	}
}

func TestUnknownUsageKeysProduceOneDebugLineAndNoWindows(t *testing.T) {
	var log bytes.Buffer
	logUnknownUsageKeys([]byte(`{"five_hour":{},"zeta":{},"alpha":{}}`), &log)
	if got, want := log.String(), "pfm usage-hook: debug: ignored usage keys: alpha,zeta\n"; got != want {
		t.Fatalf("debug log=%q, want %q", got, want)
	}
}

func TestWarnRecoverQuietTransition(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, ".claude")
	cacheDir := filepath.Join(root, "cache")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"fixture-token"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	options := Options{
		Now:       func() time.Time { return time.Unix(1_786_838_400, 0) },
		Home:      root,
		ConfigDir: configDir,
		CacheDir:  cacheDir,
		Warn:      80,
		Critical:  95,
		TTL:       24 * time.Hour,
	}
	seed := func(five, seven int) {
		t.Helper()
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			t.Fatal(err)
		}
		body := []byte(`{"config_dir":` + `"` + configDir + `",` + `"five_hour":{"utilization":` + itoa(five) +
			`,"resets_at":"2030-01-01T10:00:00Z"},` +
			`"seven_day":{"utilization":` + itoa(seven) +
			`,"resets_at":"2030-01-03T08:00:00Z"},` +
			`"seven_day_opus":{"utilization":null}}`)
		if err := os.WriteFile(filepath.Join(cacheDir, "acct-1.json"), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	seed(20, 96)
	warn, err := Evaluate(context.Background(), options)
	if err != nil || !strings.Contains(warn, "USAGE LIMIT IMMINENT") {
		t.Fatalf("warn=%q err=%v", warn, err)
	}
	seed(13, 3)
	recovered, err := Evaluate(context.Background(), options)
	if err != nil || !strings.Contains(recovered, "usage recovered") ||
		!strings.Contains(recovered, "STALE") {
		t.Fatalf("recovered=%q err=%v", recovered, err)
	}
	quiet, err := Evaluate(context.Background(), options)
	if err != nil || quiet != "" {
		t.Fatalf("quiet=%q err=%v", quiet, err)
	}
	seed(96, 20)
	rewarn, err := Evaluate(context.Background(), options)
	if err != nil || !strings.Contains(rewarn, "USAGE LIMIT IMMINENT") {
		t.Fatalf("rewarn=%q err=%v", rewarn, err)
	}
	// REGRESSION (2026-09-10, observed live): fable at 95% printed "This window is nearly
	// exhausted: finish the in-flight step, then /reload" on every prompt while the 5-hour
	// window was 1% used. Severity by max() is correct; asserting it about the LIVE window was
	// not. A model-scoped cap must name the model and report the real 5-hour headroom.
	seedNamed := func(five, seven int, fable string) {
		t.Helper()
		body := []byte(`{"config_dir":` + `"` + configDir + `",` + `"five_hour":{"utilization":` + itoa(five) +
			`,"resets_at":"2030-01-01T10:00:00Z"},` +
			`"seven_day":{"utilization":` + itoa(seven) +
			`,"resets_at":"2030-01-03T08:00:00Z"},` +
			`"seven_day_opus":{"utilization":null},` +
			`"limits":[{"kind":"weekly_scoped","scope":{"model":{"display_name":"Fable"}},` +
			`"percent":` + fable + `,"resets_at":"2030-01-03T08:00:00Z","is_active":true}]}`)
		if err := os.WriteFile(filepath.Join(cacheDir, "acct-1.json"), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	seed(5, 5)
	if _, err := Evaluate(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	seedNamed(1, 76, "95")
	scoped, err := Evaluate(context.Background(), options)
	if err != nil || !strings.Contains(scoped, "USAGE LIMIT IMMINENT") {
		t.Fatalf("scoped=%q err=%v", scoped, err)
	}
	if !strings.Contains(scoped, "Only the 7-day fable cap") ||
		!strings.Contains(scoped, "5-hour window is 1% used") ||
		!strings.Contains(scoped, "Keep working") {
		t.Fatalf("model-scoped cap must name the model and the real 5h headroom: %q", scoped)
	}
	if strings.Contains(scoped, "finish the in-flight step") {
		t.Fatalf("model-scoped cap must NOT order a session-wide stop: %q", scoped)
	}
	seedNamed(97, 76, "20")
	fiveCrit, err := Evaluate(context.Background(), options)
	if err != nil || !strings.Contains(fiveCrit, "The 5-hour window is nearly exhausted") {
		t.Fatalf("fiveCrit=%q err=%v", fiveCrit, err)
	}

	seed(5, 5)
	secondRecovery, err := Evaluate(context.Background(), options)
	if err != nil || !strings.Contains(secondRecovery, "usage recovered") {
		t.Fatalf("second recovery=%q err=%v", secondRecovery, err)
	}
	if err := os.Remove(filepath.Join(cacheDir, "warned-1")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	seed(10, 10)
	coldHealthy, err := Evaluate(context.Background(), options)
	if err != nil || coldHealthy != "" {
		t.Fatalf("cold healthy=%q err=%v", coldHealthy, err)
	}
}

// TestRecoveredWindowSaysResetPassedInsteadOfAStaleZero is the regression for
// the recovery-banner bug: five/seven come from currentUtilization(window,
// now, 0), which returns the fallback 0 for a window whose resets_at has
// already passed — "unknown, awaiting refetch", not "measured 0%". The old
// banner rendered that as a truthful-looking "back to 5h 0% · 7d 0%", and an
// expired window collapsing to 0 is exactly what drags `maximum` under
// options.Warn and enters this branch in the first place, so it is the likely
// reading, not a corner case. Reaching the recovery branch requires the
// warned-<account> flag to already exist (TestEvaluateIgnoresWindowsPastTheir
// Reset never sets it, which is why this survived); this test pre-arms it.
func TestRecoveredWindowSaysResetPassedInsteadOfAStaleZero(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, ".cc", "9")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"fixture-token"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	cacheDir := filepath.Join(root, "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Pre-arm the warned flag: without it, a low-utilization cache never
	// enters the recovery branch at all — it is simply quiet.
	if err := atomicfile.Write(filepath.Join(cacheDir, "warned-9"), []byte(configDir), 0o600); err != nil {
		t.Fatal(err)
	}
	passed := now.Add(-90 * time.Minute)
	if err := WriteCacheRecord(CachePath(cacheDir, 9), CacheRecord{
		Usage: Usage{
			FiveHour: Window{Utilization: usageFloatPtr(97), ResetsAt: passed.Format(time.RFC3339)},
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
		AccountDirs: map[string]int{configDir: 9}, CacheDir: cacheDir,
		Warn: 80, Critical: 95, TTL: 24 * time.Hour, Log: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, "usage recovered") {
		t.Fatalf("expected the recovery banner, got %q", message)
	}
	if strings.Contains(message, "0%") {
		t.Fatalf("recovery banner asserted a measured 0%% for an expired window: %q", message)
	}
	if !strings.Contains(message, "reset passed") {
		t.Fatalf("recovery banner did not say the expired window's reset passed: %q", message)
	}

	// Mirror case: a window whose reset is still in the future, at a genuine
	// low percentage, must still render its real number rather than the
	// "reset passed" phrase.
	if err := atomicfile.Write(filepath.Join(cacheDir, "warned-9"), []byte(configDir), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteCacheRecord(CachePath(cacheDir, 9), CacheRecord{
		Usage: Usage{
			FiveHour: Window{Utilization: usageFloatPtr(5), ResetsAt: now.Add(2 * time.Hour).Format(time.RFC3339)},
			SevenDay: Window{
				Utilization: usageFloatPtr(12),
				ResetsAt:    now.Add(4 * 24 * time.Hour).Format(time.RFC3339),
			},
		},
		ConfigDir: configDir, FetchedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	liveMessage, err := Evaluate(context.Background(), Options{
		Now: func() time.Time { return now }, Home: root, ConfigDir: configDir,
		AccountDirs: map[string]int{configDir: 9}, CacheDir: cacheDir,
		Warn: 80, Critical: 95, TTL: 24 * time.Hour, Log: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(liveMessage, "5h 5%") {
		t.Fatalf("recovery banner for a live window lost its real percentage: %q", liveMessage)
	}
	if strings.Contains(liveMessage, "reset passed") {
		t.Fatalf("recovery banner for a live window wrongly said reset passed: %q", liveMessage)
	}
}

func TestEvaluateDoesNotReuseAReassignedAccountCache(t *testing.T) {
	for _, testcase := range []struct {
		name       string
		cacheDirID string
		age        time.Duration
		backoff    bool
		serverFail bool
	}{
		{name: "fresh foreign identity", cacheDirID: "foreign"},
		{name: "legacy blank identity", cacheDirID: "legacy"},
		{name: "foreign backoff", cacheDirID: "foreign", backoff: true},
		{name: "foreign stale cache", cacheDirID: "foreign", age: 30 * time.Minute, serverFail: true},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			root := t.TempDir()
			configA := filepath.Join(root, ".cc", "2-a")
			configB := filepath.Join(root, ".cc", "2-b")
			for _, configDir := range []string{configA, configB} {
				if err := os.MkdirAll(configDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					filepath.Join(configDir, ".credentials.json"),
					[]byte(`{"claudeAiOauth":{"accessToken":"fixture-token"}}`),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().Truncate(time.Second)
			cacheDir := filepath.Join(root, "cache")
			cacheConfig := configA
			if testcase.cacheDirID == "legacy" {
				cacheConfig = ""
			}
			fetchedAt := now.Add(-testcase.age)
			record := CacheRecord{
				Usage: Usage{
					FiveHour: Window{
						Utilization: usageFloatPtr(96),
						ResetsAt:    now.Add(4 * time.Hour).Format(time.RFC3339),
					},
					SevenDay: Window{
						Utilization: usageFloatPtr(96),
						ResetsAt:    now.Add(6 * 24 * time.Hour).Format(time.RFC3339),
					},
				},
				ConfigDir: cacheConfig, FetchedAt: &fetchedAt,
			}
			if testcase.backoff {
				record.Backoff = &CacheBackoff{
					Message:    "429 Too Many Requests",
					RetryAfter: now.Add(time.Hour),
					RecordedAt: now,
				}
			}
			cachePath := CachePath(cacheDir, 2)
			if err := WriteCacheRecord(cachePath, record); err != nil {
				t.Fatal(err)
			}
			if testcase.age > 0 {
				old := now.Add(-testcase.age)
				if err := os.Chtimes(cachePath, old, old); err != nil {
					t.Fatal(err)
				}
			}
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				hits++
				if testcase.serverFail {
					writer.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_, _ = io.WriteString(
					writer,
					`{"five_hour":{"utilization":12,"resets_at":"2030-01-01T10:00:00Z"},"seven_day":{"utilization":3,"resets_at":"2030-01-03T08:00:00Z"}}`,
				)
			}))
			defer server.Close()
			message, err := Evaluate(context.Background(), Options{
				Now: func() time.Time { return now }, Home: root, ConfigDir: configB,
				AccountDirs: map[string]int{configA: 2, configB: 2}, CacheDir: cacheDir,
				Warn: 80, Critical: 95, TTL: 10 * time.Minute, Client: server.Client(), Endpoint: server.URL,
			})
			if testcase.serverFail {
				if err != nil || message != "" || hits != 1 {
					t.Fatalf(
						"foreign stale cache was used after refresh failure: message=%q err=%v hits=%d",
						message,
						err,
						hits,
					)
				}
				return
			}
			if err != nil || message != "" || hits != 1 {
				t.Fatalf("reassigned account cache was reused: message=%q err=%v hits=%d", message, err, hits)
			}
		})
	}
}

func TestEvaluateWarningRecoveryRequiresTheSameConfigDirectory(t *testing.T) {
	root := t.TempDir()
	configA := filepath.Join(root, ".cc", "2-a")
	configB := filepath.Join(root, ".cc", "2-b")
	for _, configDir := range []string{configA, configB} {
		if err := os.MkdirAll(configDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(configDir, ".credentials.json"),
			[]byte(`{"claudeAiOauth":{"accessToken":"fixture-token"}}`),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().Truncate(time.Second)
	cacheDir := filepath.Join(root, "cache")
	if err := WriteCacheRecord(CachePath(cacheDir, 2), CacheRecord{
		Usage: Usage{
			FiveHour: Window{Utilization: usageFloatPtr(10), ResetsAt: now.Add(4 * time.Hour).Format(time.RFC3339)},
			SevenDay: Window{
				Utilization: usageFloatPtr(10),
				ResetsAt:    now.Add(6 * 24 * time.Hour).Format(time.RFC3339),
			},
		},
		ConfigDir: configB, FetchedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.Write(filepath.Join(cacheDir, "warned-2"), []byte(configA), 0o600); err != nil {
		t.Fatal(err)
	}
	message, err := Evaluate(context.Background(), Options{
		Now: func() time.Time { return now }, Home: root, ConfigDir: configB,
		AccountDirs: map[string]int{configA: 2, configB: 2}, CacheDir: cacheDir,
		Warn: 80, Critical: 95, TTL: 24 * time.Hour,
	})
	if err != nil || message != "" {
		t.Fatalf("warning from seat %q recovered using seat %q flag: message=%q err=%v", configB, configA, message, err)
	}
}

func TestRefreshUsesHeaderAndAcceptsOnlyAUsagePayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			t.Errorf("beta header = %q", request.Header.Get("anthropic-beta"))
		}
		_, _ = io.WriteString(writer, `{
			"five_hour":{"utilization":81.9,"resets_at":"2030-01-01T10:00:00Z"},
			"seven_day":{"utilization":12,"resets_at":"2030-01-03T08:00:00Z"},
			"seven_day_opus":{"utilization":null}
		}`)
	}))
	defer server.Close()

	root := t.TempDir()
	configDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"fixture-token"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	message, err := Evaluate(context.Background(), Options{
		Now:       time.Now,
		Home:      root,
		ConfigDir: configDir,
		CacheDir:  filepath.Join(root, "cache"),
		Warn:      80,
		Critical:  95,
		TTL:       time.Minute,
		Client:    server.Client(),
		Endpoint:  server.URL,
	})
	if err != nil || !strings.Contains(message, "usage limit approaching") ||
		!strings.Contains(message, "5h 81%") {
		t.Fatalf("message=%q err=%v", message, err)
	}
	record, err := ReadCacheRecord(CachePath(filepath.Join(root, "cache"), 1))
	if err != nil {
		t.Fatalf("read refreshed cache: %v", err)
	}
	if !record.MatchesConfigDir(configDir) || record.FetchedAt == nil {
		t.Fatalf("refresh wrote unbound cache record: %#v", record)
	}
}

func TestMissingCredentialsAndPoisonPayloadFailOpen(t *testing.T) {
	root := t.TempDir()
	message, err := Evaluate(context.Background(), Options{
		Home: root, ConfigDir: filepath.Join(root, ".claude"), CacheDir: filepath.Join(root, "cache"),
	})
	if err != nil || message != "" {
		t.Fatalf("missing credentials: message=%q err=%v", message, err)
	}

	configDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"fixture"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, `{"error":"unauthorized"}`)
	}))
	defer server.Close()
	message, err = Evaluate(context.Background(), Options{
		Home: root, ConfigDir: configDir, CacheDir: filepath.Join(root, "cache"),
		Client: server.Client(), Endpoint: server.URL,
	})
	if err != nil || message != "" {
		t.Fatalf("poison payload: message=%q err=%v", message, err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "cache", "acct-1.json")); !os.IsNotExist(statErr) {
		t.Fatalf("invalid payload replaced cache: %v", statErr)
	}
}

func usageFloatPtr(value float64) *float64 { return &value }

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for value > 0 {
		digits = append(digits, byte('0'+value%10))
		value /= 10
	}
	for left, right := 0, len(digits)-1; left < right; left, right = left+1, right-1 {
		digits[left], digits[right] = digits[right], digits[left]
	}
	return string(digits)
}

// TestEvaluateAgesTheCacheByFetchedAtNotFileMtime pins the 2026-09-11 bug: a
// writer that records only a backoff rewrites the file — bumping its mtime —
// while carrying the previous payload's fetched_at forward. Aging the cache by
// mtime revived an eight-hour-old payload as "fresh" and warned from it. The
// record's own fetched_at is the only honest age; an active backoff still
// suppresses the hook's own request.
func TestEvaluateAgesTheCacheByFetchedAtNotFileMtime(t *testing.T) {
	for _, testcase := range []struct {
		name     string
		backoff  bool
		wantHits int
	}{
		{name: "refetches once no backoff is active", wantHits: 1},
		{name: "recorded backoff still suppresses the request", backoff: true},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			root := t.TempDir()
			configDir := filepath.Join(root, ".cc", "2")
			if err := os.MkdirAll(configDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(configDir, ".credentials.json"),
				[]byte(`{"claudeAiOauth":{"accessToken":"fixture-token"}}`),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			now := time.Now().Truncate(time.Second)
			fetchedAt := now.Add(-2 * time.Hour)
			cacheDir := filepath.Join(root, "cache")
			cachePath := CachePath(cacheDir, 2)
			record := CacheRecord{
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
				ConfigDir: configDir, FetchedAt: &fetchedAt,
			}
			if testcase.backoff {
				record.Backoff = &CacheBackoff{
					Message:    "429 Too Many Requests",
					RetryAfter: now.Add(time.Hour),
					RecordedAt: now,
				}
			}
			// WriteCacheRecord leaves mtime at "now" — exactly the state a
			// backoff-only write produces.
			if err := WriteCacheRecord(cachePath, record); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(cachePath)
			if err != nil {
				t.Fatal(err)
			}
			if age := time.Since(info.ModTime()); age > time.Minute {
				t.Fatalf("fixture mtime age=%s, want a just-written file", age)
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
				Warn: 80, Critical: 95, TTL: 10 * time.Minute,
				Client: server.Client(), Endpoint: server.URL, Log: io.Discard,
			})
			if err != nil {
				t.Fatal(err)
			}
			if message != "" {
				t.Fatalf("two-hour-old payload warned as fresh: %q", message)
			}
			if hits != testcase.wantHits {
				t.Fatalf("provider requests=%d, want %d", hits, testcase.wantHits)
			}
		})
	}
}

// TestEvaluateIgnoresWindowsPastTheirReset applies fableWindow's rule to the
// 5h and 7d windows: a cached reading whose resets_at has already passed
// describes quota that has since rolled over and must never raise the warning.
func TestEvaluateIgnoresWindowsPastTheirReset(t *testing.T) {
	for _, testcase := range []struct {
		name         string
		fiveResetsAt time.Duration
		wantWarning  bool
	}{
		{name: "passed reset stays silent", fiveResetsAt: -time.Minute},
		{name: "live reset still warns", fiveResetsAt: time.Hour, wantWarning: true},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			root := t.TempDir()
			configDir := filepath.Join(root, ".cc", "3")
			if err := os.MkdirAll(configDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(configDir, ".credentials.json"),
				[]byte(`{"claudeAiOauth":{"accessToken":"fixture-token"}}`),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			now := time.Now().Truncate(time.Second)
			cacheDir := filepath.Join(root, "cache")
			if err := WriteCacheRecord(CachePath(cacheDir, 3), CacheRecord{
				Usage: Usage{
					FiveHour: Window{
						Utilization: usageFloatPtr(98),
						ResetsAt:    now.Add(testcase.fiveResetsAt).Format(time.RFC3339),
					},
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
				AccountDirs: map[string]int{configDir: 3}, CacheDir: cacheDir,
				Warn: 80, Critical: 95, TTL: 24 * time.Hour, Log: io.Discard,
			})
			if err != nil {
				t.Fatal(err)
			}
			if warned := strings.Contains(message, "USAGE LIMIT IMMINENT"); warned != testcase.wantWarning {
				t.Fatalf("warning=%v want %v: message=%q", warned, testcase.wantWarning, message)
			}
		})
	}
}

// TestWarnLineSaysResetPassedInsteadOfAStalePercentage covers the warn line
// itself: when one window is expired and a LIVE one raises the warning, the
// expired clause must not advertise its rolled-over percentage or a reset
// moment already in the past — it reads "5h — (reset passed)".
func TestWarnLineSaysResetPassedInsteadOfAStalePercentage(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, ".cc", "8")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"fixture-token"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	cacheDir := filepath.Join(root, "cache")
	passed := now.Add(-90 * time.Minute)
	if err := WriteCacheRecord(CachePath(cacheDir, 8), CacheRecord{
		Usage: Usage{
			FiveHour: Window{Utilization: usageFloatPtr(97), ResetsAt: passed.Format(time.RFC3339)},
			SevenDay: Window{
				Utilization: usageFloatPtr(88),
				ResetsAt:    now.Add(4 * 24 * time.Hour).Format(time.RFC3339),
			},
		},
		ConfigDir: configDir, FetchedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	message, err := Evaluate(context.Background(), Options{
		Now: func() time.Time { return now }, Home: root, ConfigDir: configDir,
		AccountDirs: map[string]int{configDir: 8}, CacheDir: cacheDir,
		Warn: 80, Critical: 95, TTL: 24 * time.Hour, Log: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, "5h — (reset passed)") {
		t.Fatalf("expired 5h clause did not say the reset passed: %q", message)
	}
	if strings.Contains(message, "5h 0%") || strings.Contains(message, "5h 97%") ||
		strings.Contains(message, "resets "+passed.Format("15:04")) {
		t.Fatalf("expired 5h clause kept a stale number or a past reset time: %q", message)
	}
	if !strings.Contains(message, "7d 88% (resets ") {
		t.Fatalf("live 7d clause lost its percentage and reset: %q", message)
	}
}
