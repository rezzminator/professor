package stats

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

// an empty or unparsable resets_at is UNKNOWN, never expired. The row
// keeps its reading and the payload stays reusable — otherwise a provider that
// omits resets_at would blank the card AND defeat the shared cache on every
// sample.
func TestUsageWindowsTreatsUnknownResetAsUnknownNotExpired(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	used := 73.0
	for _, resets := range []string{"", "not-a-timestamp", "2026-13-45T99:99:99Z"} {
		usage := usagehook.Usage{
			FiveHour: usagehook.Window{Utilization: &used, ResetsAt: resets},
			SevenDay: usagehook.Window{Utilization: &used, ResetsAt: resets},
		}
		windows := usageWindows(usage, now)
		if len(windows) != 2 {
			t.Fatalf("resets_at=%q: windows=%#v, want both rows", resets, windows)
		}
		for _, window := range windows {
			if window.UsedPct != 73 || window.ResetNote == expiredResetNote {
				t.Fatalf("resets_at=%q: window=%#v, want the reading kept and no expiry note", resets, window)
			}
		}
		if !reusableClaudeUsage(usage, now) {
			t.Fatalf("resets_at=%q: payload with unknown resets was treated as expired", resets)
		}
	}
}

// the 60s live TTL must hold across the shared DISK cache, not only the
// sampler's in-memory map. Two sampler values are two `pfm` processes on one
// host — a second picker opened 59s after the first must not spend a request,
// and neither may the same picker polled every two seconds (the cadence a
// keystroke on the Limits tab restores).
func TestClaudeLiveTTLHoldsAcrossProcessesAndKeypressPolling(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	configDir := filepath.Join(home, "claude")
	writeFixtureCredentials(t, configDir)
	now := time.Unix(1_800_000_000, 0)
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, usageJSONBody(10, 20, now))
	}))
	defer server.Close()
	account := LimitAccount{ID: 31, Engine: pfmengine.Claude, Label: "account 31", ConfigDir: configDir}
	newSampler := func(clock func() time.Time) *LimitsSampler {
		sampler := NewLimitsSampler([]LimitAccount{account})
		sampler.TTL = LiveLimitsTTL
		sampler.CodexTTL = CodexLiveLimitsTTL
		sampler.Endpoint = server.URL
		sampler.Now = clock
		return sampler
	}

	first := newSampler(func() time.Time { return now })
	if _, warnings := first.Sample(context.Background()); len(warnings) != 0 || hits != 1 {
		t.Fatalf("first sample: hits=%d warnings=%v", hits, warnings)
	}
	// Same process, polled every two seconds for the whole TTL.
	for offset := 2 * time.Second; offset < LiveLimitsTTL; offset += 2 * time.Second {
		at := now.Add(offset)
		poller := newSampler(func() time.Time { return at })
		poller.Sample(context.Background())
		first.Now = func() time.Time { return at }
		first.Sample(context.Background())
		if hits != 1 {
			t.Fatalf("poll at +%s spent a request: hits=%d, want 1 inside the %s TTL", offset, hits, LiveLimitsTTL)
		}
	}
	// A peer process one second past the TTL is allowed to refetch.
	past := now.Add(LiveLimitsTTL + time.Second)
	peer := newSampler(func() time.Time { return past })
	peer.Sample(context.Background())
	if hits != 2 {
		t.Fatalf("peer past the TTL: hits=%d, want exactly 2", hits)
	}
}

// the legacy (`pfm ls --json`, `pfm doctor`, prompt hook) sampler keeps
// the three-minute TTL — it must never inherit the picker's cadence.
func TestLegacySamplerKeepsTheThreeMinuteTTL(t *testing.T) {
	sampler := NewLimitsSampler(nil)
	if sampler.TTL != defaultLimitsTTL || defaultLimitsTTL != 3*time.Minute {
		t.Fatalf("legacy TTL=%s (default %s), want 3m", sampler.TTL, defaultLimitsTTL)
	}
	if sampler.ttl() != 3*time.Minute || sampler.ttlFor(pfmengine.Codex) != 3*time.Minute {
		t.Fatalf(
			"ttl=%s codex=%s, want 3m for both without an explicit override",
			sampler.ttl(),
			sampler.ttlFor(pfmengine.Codex),
		)
	}
	if LiveLimitsTTL < 60*time.Second {
		t.Fatalf("LiveLimitsTTL=%s, want at least 60s (2026-09-11 rate-limit incident)", LiveLimitsTTL)
	}
}

// the whole seam end to end. A 429 makes fetchClaudeCached write a
// BACKOFF-ONLY record: last-good payload, its ORIGINAL fetched_at, a new
// mtime. The prompt hook reading that same file must age it by fetched_at —
// two hours — and therefore neither warn from it nor spend a request of its
// own while the backoff is in force.
func TestBackoffOnlyWriteByStatsIsNotFreshForThePromptHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	configDir := filepath.Join(home, "claude")
	writeFixtureCredentials(t, configDir)
	now := time.Now().Truncate(time.Second)
	account := LimitAccount{ID: 9, Engine: pfmengine.Claude, Label: "account 9", ConfigDir: configDir}
	cachePath := usagehook.CachePath(usagehook.DefaultCacheDir(), account.ID)
	fetchedAt := now.Add(-2 * time.Hour)
	hot := 99.0
	if err := usagehook.WriteCacheRecord(cachePath, usagehook.CacheRecord{
		Usage: usagehook.Usage{
			FiveHour: usagehook.Window{Utilization: &hot, ResetsAt: now.Add(2 * time.Hour).Format(time.RFC3339)},
			SevenDay: usagehook.Window{Utilization: &hot, ResetsAt: now.Add(72 * time.Hour).Format(time.RFC3339)},
		},
		ConfigDir: configDir, FetchedAt: &fetchedAt,
	}); err != nil {
		t.Fatal(err)
	}
	var statsHits int
	rateLimited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		statsHits++
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer rateLimited.Close()
	sampler := NewLimitsSampler([]LimitAccount{account})
	sampler.TTL = LiveLimitsTTL
	sampler.Now = func() time.Time { return now }
	sampler.Endpoint = rateLimited.URL
	sampler.Client = rateLimited.Client()
	sampler.Sample(context.Background())
	if statsHits != 1 {
		t.Fatalf("stats sampler hits=%d, want the single 429 that records the backoff", statsHits)
	}
	record, err := usagehook.ReadCacheRecord(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if record.Backoff == nil || record.FetchedAt == nil || !record.FetchedAt.Equal(fetchedAt) {
		t.Fatalf("backoff-only write=%#v, want the original fetched_at carried forward", record)
	}

	var hookHits int
	hookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hookHits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, usageJSONBody(1, 1, now))
	}))
	defer hookServer.Close()
	message, err := usagehook.Evaluate(context.Background(), usagehook.Options{
		Now: func() time.Time { return now }, Home: home, ConfigDir: configDir,
		AccountDirs: map[string]int{configDir: account.ID},
		CacheDir:    usagehook.DefaultCacheDir(),
		Warn:        80, Critical: 95, TTL: 3 * time.Minute,
		Client: hookServer.Client(), Endpoint: hookServer.URL, Log: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if message != "" {
		t.Fatalf("hook warned from a two-hour-old payload under an active backoff: %q", message)
	}
	if hookHits != 0 {
		t.Fatalf("hook spent %d request(s) through an active 429 backoff, want 0", hookHits)
	}
}

// an all-expired cached payload under an active 429 must not reach the
// provider through the LIVE picker path either, and the card must not present
// the rolled-over readings as current.
func TestExpiredWindowsUnderBackoffNeverFetchOnTheLivePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	configDir := filepath.Join(home, "claude")
	writeFixtureCredentials(t, configDir)
	now := time.Unix(1_800_000_000, 0)
	account := LimitAccount{ID: 12, Engine: pfmengine.Claude, Label: "account 12", ConfigDir: configDir}
	fetchedAt := now.Add(-30 * time.Second)
	hot := 96.0
	if err := usagehook.WriteCacheRecord(
		usagehook.CachePath(usagehook.DefaultCacheDir(), account.ID),
		usagehook.CacheRecord{
			Usage: usagehook.Usage{
				FiveHour: usagehook.Window{Utilization: &hot, ResetsAt: now.Add(-time.Second).Format(time.RFC3339)},
				SevenDay: usagehook.Window{Utilization: &hot, ResetsAt: now.Add(-time.Hour).Format(time.RFC3339)},
			},
			ConfigDir: configDir, FetchedAt: &fetchedAt,
			Backoff: &usagehook.CacheBackoff{
				Message:    "limits unavailable: 429 Too Many Requests",
				RetryAfter: now.Add(time.Hour),
				RecordedAt: fetchedAt,
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, usageJSONBody(5, 5, now))
	}))
	defer server.Close()
	sampler := NewLimitsSampler([]LimitAccount{account})
	sampler.TTL = LiveLimitsTTL
	sampler.CodexTTL = CodexLiveLimitsTTL
	sampler.Now = func() time.Time { return now }
	sampler.Endpoint = server.URL
	sampler.Client = server.Client()
	// Poll the live path the way the picker does while the tab is watched.
	for offset := time.Duration(0); offset < 10*time.Second; offset += 2 * time.Second {
		sampler.SampleLive(context.Background())
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	limits, _ := sampler.SampleLive(context.Background())
	if hits != 0 {
		t.Fatalf("live picker bypassed an active 429 backoff: hits=%d", hits)
	}
	for _, window := range limits[0].Windows {
		if window.UsedPct == 96 {
			t.Fatalf("rolled-over reading presented as current: %#v", window)
		}
	}
}
