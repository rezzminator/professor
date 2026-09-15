package statusline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/usagehook"
)

// fableRuntime builds a jailed Runtime for account, with an explicit
// AccountDirs entry so account resolution never depends on the real
// filesystem or a legacy-path fallback — the same determinism
// TestStatuslineQuotaSnapshotCarriesAccountConfigIdentity relies on.
func fableRuntime(root string, account int, now time.Time) Runtime {
	configDir := filepath.Join(root, ".cc", strconv.Itoa(account))
	return Runtime{
		Now:          func() time.Time { return now },
		Home:         root,
		ConfigDir:    configDir,
		CacheDir:     filepath.Join(root, "cache"),
		RateLimitDir: filepath.Join(root, "rates"),
		SIDDir:       filepath.Join(root, "sid"),
		TmuxDir:      filepath.Join(root, "tmux"),
		ProcRoot:     filepath.Join(root, "proc"),
		Columns:      120,
		UID:          1000,
		AccountDirs:  map[string]int{configDir: account},
		Env:          map[string]string{},
		Command:      quietRunner{},
	}
}

// writeFableCacheRecord seeds the exact file CachedFableWindow reads, through
// the package's own writer, so the test never re-derives the "cc-usage-<uid>"
// naming or the on-disk shape independently of the code under test.
func writeFableCacheRecord(t *testing.T, cacheDir string, uid, account int, record usagehook.CacheRecord) {
	t.Helper()
	path := usagehook.CachePath(usagehook.UsageCacheDir(cacheDir, uid), account)
	if err := usagehook.WriteCacheRecord(path, record); err != nil {
		t.Fatalf("seed usage cache: %v", err)
	}
}

func fableScopedLimit(percent float64, resetsAt string, active bool) usagehook.ScopedLimit {
	limit := usagehook.ScopedLimit{Kind: "weekly_scoped", Percent: &percent, ResetsAt: resetsAt, IsActive: active}
	limit.Scope.Model.DisplayName = "Fable"
	return limit
}

func floatPtr(value float64) *float64 { return &value }

func stripANSICodes(raw string) string {
	return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(raw, "")
}

// harnessPayloadWithFiveAndSeven builds a statusline stdin payload carrying
// five_hour/seven_day (so the rate-limit block is non-empty and therefore
// renders — appendRateSegments renders nothing at all when the block is
// completely empty, which would make an absent fable marker unobservable),
// plus whatever raw `limits` fragment the caller supplies. limitsFragment=""
// omits the "limits" key entirely (Scoped stays nil); any other value is
// inserted verbatim as the `"limits":...` member.
func harnessPayloadWithFiveAndSeven(now time.Time, limitsFragment string) []byte {
	limits := ""
	if limitsFragment != "" {
		limits = `,"limits":` + limitsFragment
	}
	return []byte(`{"model":{"display_name":"Opus 4"},"rate_limits":{` +
		`"five_hour":{"used_percentage":11,"resets_at":` + strconv.FormatInt(now.Add(4*time.Hour).Unix(), 10) + `},` +
		`"seven_day":{"used_percentage":31,"resets_at":` + strconv.FormatInt(now.Add(6*24*time.Hour).Unix(), 10) + `}` +
		limits + `}}`)
}

// TestFableWindowFallsBackToUsageCacheWhenHarnessOmitsLimits is the
// regression test for the user's exact symptom: a harness payload with no
// `limits` key at all left the 7d-fable window permanently at "—" even
// though pfm's own usage cache already held a qualifying Fable entry. It
// must fail against the unfixed windowsAt (see report for the watched
// failure).
func TestFableWindowFallsBackToUsageCacheWhenHarnessOmitsLimits(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	runtime := fableRuntime(root, 2, now)
	writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
		Usage: usagehook.Usage{
			Limits: []usagehook.ScopedLimit{
				fableScopedLimit(38, now.Add(7*24*time.Hour).UTC().Format(time.RFC3339), true),
			},
		},
		ConfigDir: runtime.ConfigDir,
		FetchedAt: &now,
	})
	got, err := Render(context.Background(), harnessPayloadWithFiveAndSeven(now, ""), runtime)
	if err != nil {
		t.Fatal(err)
	}
	plain := stripANSICodes(got)
	if !strings.Contains(plain, "7d-fable-used:38%") {
		t.Fatalf(
			"fable window did not fall back to pfm's own usage cache when the harness sent no limits array:\n%q",
			plain,
		)
	}
}

// TestFableWindowPrefersHarnessLimitsOverCache pins precedence rule #1: a
// harness payload that DOES carry a `limits` array wins outright, even when
// pfm's cache holds a different value for the same account — the cache is a
// fallback for an omitted array, never a second opinion.
func TestFableWindowPrefersHarnessLimitsOverCache(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	runtime := fableRuntime(root, 2, now)
	writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
		Usage: usagehook.Usage{
			Limits: []usagehook.ScopedLimit{
				fableScopedLimit(90, now.Add(7*24*time.Hour).UTC().Format(time.RFC3339), true),
			},
		},
		ConfigDir: runtime.ConfigDir,
		FetchedAt: &now,
	})
	limits := `[{"kind":"weekly_scoped","percent":23,"resets_at":"` +
		now.Add(7*24*time.Hour).
			UTC().
			Format(time.RFC3339) +
		`","scope":{"model":{"display_name":"Fable"}},"is_active":true}]`
	got, err := Render(context.Background(), harnessPayloadWithFiveAndSeven(now, limits), runtime)
	if err != nil {
		t.Fatal(err)
	}
	plain := stripANSICodes(got)
	if !strings.Contains(plain, "7d-fable-used:23%") {
		t.Fatalf("harness-reported fable window lost to the cache:\n%q", plain)
	}
	if strings.Contains(plain, "90%") {
		t.Fatalf("cache value leaked past a harness payload that DID carry limits:\n%q", plain)
	}
}

// TestFableWindowPresentButEmptyLimitsArrayIsNotCacheFallback pins the
// nil-vs-empty distinction windowsAt's own comment relies on: json decodes a
// present `"limits":[]` into a non-nil, zero-length slice, which must NOT
// trip the "harness omitted the array" fallback — a harness that explicitly
// reported and simply has no Fable entry for this account is not the same
// fact as a harness that never sent the array at all.
func TestFableWindowPresentButEmptyLimitsArrayIsNotCacheFallback(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	runtime := fableRuntime(root, 2, now)
	writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
		Usage: usagehook.Usage{
			Limits: []usagehook.ScopedLimit{
				fableScopedLimit(77, now.Add(7*24*time.Hour).UTC().Format(time.RFC3339), true),
			},
		},
		ConfigDir: runtime.ConfigDir,
		FetchedAt: &now,
	})
	got, err := Render(context.Background(), harnessPayloadWithFiveAndSeven(now, "[]"), runtime)
	if err != nil {
		t.Fatal(err)
	}
	plain := stripANSICodes(got)
	if !strings.Contains(plain, "7d-fable-used:—") {
		t.Fatalf("a present-but-empty limits array did not render absent:\n%q", plain)
	}
	if strings.Contains(plain, "77%") {
		t.Fatalf("a present-but-empty limits array consulted the cache anyway:\n%q", plain)
	}
}

// TestFableWindowAbsentWithNoUsageCacheFile pins the plain missing-cache
// path: no cache file at all degrades to absent, and Render never errors —
// the honesty requirement that a cache-read problem must never propagate out
// of Render.
func TestFableWindowAbsentWithNoUsageCacheFile(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	runtime := fableRuntime(root, 2, now)
	got, err := Render(context.Background(), harnessPayloadWithFiveAndSeven(now, ""), runtime)
	if err != nil {
		t.Fatalf("Render returned an error for a missing usage cache: %v", err)
	}
	plain := stripANSICodes(got)
	if !strings.Contains(plain, "7d-fable-used:—") {
		t.Fatalf("missing usage cache did not render absent:\n%q", plain)
	}
}

// TestFableWindowMalformedOrUnreadableCacheDegradesToAbsentWithoutBreakingTheLine
// pins the honesty requirement that a broken cache must never blank the
// statusline: Render still succeeds, the fable window degrades to absent,
// and the rest of the rate-limit line (five_hour/seven_day, sourced only
// from the harness) stays intact. Both a malformed file and a structurally
// unreadable one are covered, because the fence runs every test as root,
// where chmod-based unreadability is a silent no-op (root ignores mode
// bits) — a directory sitting where the cache file must be fails
// os.ReadFile for every uid instead.
func TestFableWindowMalformedOrUnreadableCacheDegradesToAbsentWithoutBreakingTheLine(t *testing.T) {
	for _, testcase := range []struct {
		name    string
		corrupt func(t *testing.T, path string)
	}{
		{
			name: "truncated JSON",
			corrupt: func(t *testing.T, path string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(`{"limits":[{"kind":"weekly_sc`), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "cache path is a directory, not a file",
			corrupt: func(t *testing.T, path string) {
				t.Helper()
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			root := t.TempDir()
			now := time.Now()
			runtime := fableRuntime(root, 2, now)
			path := usagehook.CachePath(usagehook.UsageCacheDir(runtime.CacheDir, runtime.UID), 2)
			testcase.corrupt(t, path)
			got, err := Render(context.Background(), harnessPayloadWithFiveAndSeven(now, ""), runtime)
			if err != nil {
				t.Fatalf("Render returned an error for a broken usage cache: %v", err)
			}
			plain := stripANSICodes(got)
			if !strings.Contains(plain, "7d-fable-used:—") {
				t.Fatalf("broken usage cache did not degrade to absent:\n%q", plain)
			}
			if !strings.Contains(plain, "5h-used:11%") || !strings.Contains(plain, "7d-used:31%") {
				t.Fatalf("broken usage cache blanked the rest of the rate-limit line:\n%q", plain)
			}
		})
	}
}

// TestFableWindowAbsentWhenUsageCacheIsBeyondTheStalenessBound pins the
// one-hour bound CachedFableWindow's doc comment commits to (matching
// Evaluate's own "a stale cache remains usable for about an hour"): a cache
// older than that renders absent, never the stale percentage. A cache still
// inside the bound is asserted alongside it, so the boundary is pinned in
// both directions rather than just the "eventually gives up" half.
func TestFableWindowAbsentWhenUsageCacheIsBeyondTheStalenessBound(t *testing.T) {
	for _, testcase := range []struct {
		name       string
		age        time.Duration
		wantAbsent bool
	}{
		{name: "just inside the one-hour bound", age: 59 * time.Minute, wantAbsent: false},
		{name: "beyond the one-hour bound", age: 90 * time.Minute, wantAbsent: true},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			root := t.TempDir()
			now := time.Now()
			runtime := fableRuntime(root, 2, now)
			fetchedAt := now.Add(-testcase.age)
			writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
				Usage: usagehook.Usage{
					Limits: []usagehook.ScopedLimit{
						fableScopedLimit(44, now.Add(7*24*time.Hour).UTC().Format(time.RFC3339), true),
					},
				},
				ConfigDir: runtime.ConfigDir,
				FetchedAt: &fetchedAt,
			})
			got, err := Render(context.Background(), harnessPayloadWithFiveAndSeven(now, ""), runtime)
			if err != nil {
				t.Fatal(err)
			}
			plain := stripANSICodes(got)
			rendered := strings.Contains(plain, "7d-fable-used:44%")
			absent := strings.Contains(plain, "7d-fable-used:—")
			if testcase.wantAbsent && !absent {
				t.Fatalf("a cache beyond the one-hour bound rendered a stale percentage:\n%q", plain)
			}
			if !testcase.wantAbsent && !rendered {
				t.Fatalf("a cache still inside the one-hour bound rendered absent:\n%q", plain)
			}
		})
	}
}

// TestFableWindowAbsentWhileUsageCacheBackoffIsActive pins that a live
// Backoff — another picker's already-recorded 429 or other failure — is
// treated as "no fresh data" even when the cache is otherwise fresh and
// carries an entirely valid Fable entry.
func TestFableWindowAbsentWhileUsageCacheBackoffIsActive(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	runtime := fableRuntime(root, 2, now)
	writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
		Usage: usagehook.Usage{
			Limits: []usagehook.ScopedLimit{
				fableScopedLimit(61, now.Add(7*24*time.Hour).UTC().Format(time.RFC3339), true),
			},
		},
		ConfigDir: runtime.ConfigDir,
		FetchedAt: &now,
		Backoff:   &usagehook.CacheBackoff{Message: "429", RetryAfter: now.Add(15 * time.Minute), RecordedAt: now},
	})
	got, err := Render(context.Background(), harnessPayloadWithFiveAndSeven(now, ""), runtime)
	if err != nil {
		t.Fatal(err)
	}
	plain := stripANSICodes(got)
	if !strings.Contains(plain, "7d-fable-used:—") {
		t.Fatalf("an active backoff did not suppress the cached fable window:\n%q", plain)
	}
	if strings.Contains(plain, "61%") {
		t.Fatalf("an active backoff still leaked the cached percentage:\n%q", plain)
	}
}

// TestFableWindowAbsentWhenCachedEntryResetsInThePast pins that the cache
// path cannot bypass fableWindow's own selection rules: a cached entry whose
// resets_at has already passed is rejected by the SAME selector a live fetch
// uses, exactly like TestScopedFableRequiresAValidFutureReset pins it for
// the harness path in usagehook's own tests.
func TestFableWindowAbsentWhenCachedEntryResetsInThePast(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	runtime := fableRuntime(root, 2, now)
	writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
		Usage: usagehook.Usage{
			Limits: []usagehook.ScopedLimit{fableScopedLimit(44, now.Add(-time.Hour).UTC().Format(time.RFC3339), true)},
		},
		ConfigDir: runtime.ConfigDir,
		FetchedAt: &now,
	})
	got, err := Render(context.Background(), harnessPayloadWithFiveAndSeven(now, ""), runtime)
	if err != nil {
		t.Fatal(err)
	}
	plain := stripANSICodes(got)
	if !strings.Contains(plain, "7d-fable-used:—") {
		t.Fatalf("a cached entry with a past resets_at was not rejected by the selector:\n%q", plain)
	}
	if strings.Contains(plain, "44%") {
		t.Fatalf("a cached entry with a past resets_at still rendered:\n%q", plain)
	}
}

// TestFableWindowNeverSourcesFiveHourOrSevenDayFromCache pins precedence
// rule #1 from the other direction: five_hour and seven_day always come from
// the harness payload, never from pfm's own cache, even when the cache holds
// different values for both AND a valid Fable entry that IS legitimately
// consulted in the same render.
func TestFableWindowNeverSourcesFiveHourOrSevenDayFromCache(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	runtime := fableRuntime(root, 2, now)
	writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
		Usage: usagehook.Usage{
			FiveHour: usagehook.Window{
				Utilization: floatPtr(77),
				ResetsAt:    now.Add(4 * time.Hour).UTC().Format(time.RFC3339),
			},
			SevenDay: usagehook.Window{
				Utilization: floatPtr(66),
				ResetsAt:    now.Add(6 * 24 * time.Hour).UTC().Format(time.RFC3339),
			},
			Limits: []usagehook.ScopedLimit{
				fableScopedLimit(38, now.Add(7*24*time.Hour).UTC().Format(time.RFC3339), true),
			},
		},
		ConfigDir: runtime.ConfigDir,
		FetchedAt: &now,
	})
	got, err := Render(context.Background(), harnessPayloadWithFiveAndSeven(now, ""), runtime)
	if err != nil {
		t.Fatal(err)
	}
	plain := stripANSICodes(got)
	if !strings.Contains(plain, "5h-used:11%") || !strings.Contains(plain, "7d-used:31%") {
		t.Fatalf("harness five_hour/seven_day values were lost:\n%q", plain)
	}
	if strings.Contains(plain, "77%") || strings.Contains(plain, "66%") {
		t.Fatalf("five_hour/seven_day were sourced from pfm's own cache instead of the harness payload:\n%q", plain)
	}
	if !strings.Contains(plain, "7d-fable-used:38%") {
		t.Fatalf("the legitimately-consulted fable window went missing alongside the cache-isolation check:\n%q", plain)
	}
}

func TestFableWindowRejectsCacheFromAnotherSeatOrLegacyRecord(t *testing.T) {
	for _, testcase := range []struct {
		name      string
		configDir string
	}{
		{name: "another seat", configDir: "other-seat"},
		{name: "legacy blank identity", configDir: ""},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			root := t.TempDir()
			now := time.Now()
			runtime := fableRuntime(root, 2, now)
			writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
				Usage: usagehook.Usage{
					Limits: []usagehook.ScopedLimit{
						fableScopedLimit(88, now.Add(7*24*time.Hour).UTC().Format(time.RFC3339), true),
					},
				},
				ConfigDir: testcase.configDir,
				FetchedAt: &now,
			})
			got, err := Render(context.Background(), harnessPayloadWithFiveAndSeven(now, ""), runtime)
			if err != nil {
				t.Fatal(err)
			}
			plain := stripANSICodes(got)
			if !strings.Contains(plain, "7d-fable-used:—") || strings.Contains(plain, "88%") {
				t.Fatalf("cache identity %q leaked into seat %q:\n%s", testcase.configDir, runtime.ConfigDir, plain)
			}
		})
	}
}

func TestHarvestRateLimitsPersistsAValidFableWithoutUsableFlatWindows(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	for _, testcase := range []struct {
		name         string
		five, seven  int
		fiveReset    time.Time
		sevenReset   time.Time
		fable        int
		fableReset   time.Time
		wantSnapshot bool
	}{
		{name: "fable only", fiveReset: now.Add(-time.Hour), sevenReset: now.Add(-time.Hour), fable: 62, fableReset: now.Add(5 * 24 * time.Hour), wantSnapshot: true},
		{name: "zero flat usage", fiveReset: now.Add(4 * time.Hour), sevenReset: now.Add(6 * 24 * time.Hour), fable: 62, fableReset: now.Add(5 * 24 * time.Hour), wantSnapshot: true},
		{name: "expired five hour", five: 50, fiveReset: now.Add(-time.Minute), sevenReset: now.Add(-time.Hour), fable: 62, fableReset: now.Add(5 * 24 * time.Hour), wantSnapshot: true},
		{name: "fable zero", fiveReset: now.Add(-time.Hour), sevenReset: now.Add(-time.Hour), fable: 0, fableReset: now.Add(5 * 24 * time.Hour), wantSnapshot: true},
		{name: "expired fable", fiveReset: now.Add(-time.Hour), sevenReset: now.Add(-time.Hour), fable: 62, fableReset: now.Add(-time.Minute), wantSnapshot: false},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			root := t.TempDir()
			runtime := fableRuntime(root, 2, now)
			input := []byte(
				fmt.Sprintf(
					`{"model":{"display_name":"Opus 4"},"rate_limits":{"five_hour":{"used_percentage":%d,"resets_at":%d},"seven_day":{"used_percentage":%d,"resets_at":%d},"limits":[{"kind":"weekly_scoped","scope":{"model":{"display_name":"Fable"}},"percent":%d,"resets_at":%q,"is_active":true}]}}`,
					testcase.five,
					testcase.fiveReset.Unix(),
					testcase.seven,
					testcase.sevenReset.Unix(),
					testcase.fable,
					testcase.fableReset.UTC().Format(time.RFC3339),
				),
			)
			if _, err := Render(context.Background(), input, runtime); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(runtime.RateLimitDir, "acct-2.anon.json")
			body, err := os.ReadFile(path)
			if !testcase.wantSnapshot {
				if !os.IsNotExist(err) {
					t.Fatalf("expired Fable authorized snapshot: read error=%v body=%q", err, body)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid Fable did not authorize snapshot: %v", err)
			}
			var snapshot struct {
				FableUsed     int64 `json:"fable_used"`
				FableResetsAt int64 `json:"fable_resets_at"`
			}
			if err := json.Unmarshal(body, &snapshot); err != nil {
				t.Fatal(err)
			}
			if snapshot.FableUsed != int64(testcase.fable) || snapshot.FableResetsAt != testcase.fableReset.Unix() {
				t.Fatalf(
					"snapshot=%#v, want Fable %d resetting at %d",
					snapshot,
					testcase.fable,
					testcase.fableReset.Unix(),
				)
			}
		})
	}
}
