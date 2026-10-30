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

	"github.com/rezzminator/professor/pfm/internal/usagehook"
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

// writeFableCacheRecord seeds the shared usage cache the renderer reads, through
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
// harness payload that DOES carry a `limits` array wins over a cache reading of
// an earlier window, however much higher — the cache outranks a reported window
// only with a newer reading of it (TestAFrozenPayloadYieldsToANewerReadingOfItsWindow).
func TestFableWindowPrefersHarnessLimitsOverCache(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	runtime := fableRuntime(root, 2, now)
	writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
		Usage: usagehook.Usage{
			Limits: []usagehook.ScopedLimit{
				fableScopedLimit(90, now.Add(time.Hour).UTC().Format(time.RFC3339), true),
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

// TestHarnessMainWindowsTakePrecedenceOverCache pins that reported harness
// values win over a cache holding earlier windows and a usable Fable entry.
func TestHarnessMainWindowsTakePrecedenceOverCache(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	runtime := fableRuntime(root, 2, now)
	writeFableCacheRecord(t, runtime.CacheDir, runtime.UID, 2, usagehook.CacheRecord{
		Usage: usagehook.Usage{
			FiveHour: usagehook.Window{
				Utilization: floatPtr(77),
				ResetsAt:    now.Add(time.Hour).UTC().Format(time.RFC3339),
			},
			SevenDay: usagehook.Window{
				Utilization: floatPtr(66),
				ResetsAt:    now.Add(24 * time.Hour).UTC().Format(time.RFC3339),
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

func TestRecordRateLimitsPersistsAValidFableWithoutUsableFlatWindows(t *testing.T) {
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
