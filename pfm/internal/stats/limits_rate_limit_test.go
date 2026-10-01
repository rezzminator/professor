package stats

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

// TestLimitsSamplerBacksOff429AcrossProcessesForAtLeastTenMinutes pins the
// 429-backoff half of the shared-cache contract: a rate-limited response must
// be recorded where every sampler can see it, and a fresh sampler standing in
// for a second process must not repeat the request that just got rate-limited.
//
// Fails at HEAD for the same reason as TestLimitsSamplerSharesFetchAcrossProcessesWithinTTL: sampler
// B starts with an empty in-memory cache and has no shared record of A's 429,
// so it fetches again — hits ends at 2, not 1.
func TestLimitsSamplerBacksOff429AcrossProcessesForAtLeastTenMinutes(t *testing.T) {
	configDir := t.TempDir()
	writeFixtureCredentials(t, configDir)
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	account := LimitAccount{ID: 5, Engine: pfmengine.Claude, Label: "account 5", ConfigDir: configDir}

	samplerA := NewLimitsSampler([]LimitAccount{account})
	samplerA.Endpoint = server.URL
	limitsA, _ := samplerA.Sample(context.Background())
	if hits != 1 || len(limitsA) != 1 || !strings.Contains(limitsA[0].Status, "rate-limited — retry ") {
		t.Fatalf("sampler A: hits=%d limits=%#v, want one recorded 429", hits, limitsA)
	}

	samplerB := NewLimitsSampler([]LimitAccount{account})
	samplerB.Endpoint = server.URL
	limitsB, _ := samplerB.Sample(context.Background())
	if hits != 1 {
		t.Fatalf("sampler B retried during the shared 429 backoff window: hits=%d, want 1 (no new request)", hits)
	}
	if len(limitsB) != 1 || !strings.Contains(limitsB[0].Status, "rate-limited — retry ") {
		t.Fatalf("sampler B limits=%#v, want the shared 429 status surfaced without a fetch", limitsB)
	}
}

func TestLimitsSampler429StatusSaysUnavailableOnceWithTheRetryTimeFirst(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	configDir := filepath.Join(home, ".claude")
	writeFixtureCredentials(t, configDir)
	now := time.Unix(1_800_000_000, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	account := LimitAccount{ID: 6, Engine: pfmengine.Claude, Label: "account 6", ConfigDir: configDir}
	sampler := NewLimitsSampler([]LimitAccount{account})
	sampler.Endpoint = server.URL
	sampler.Now = func() time.Time { return now }
	limits, _ := sampler.Sample(context.Background())
	if len(limits) != 1 {
		t.Fatalf("limits=%#v, want one card", limits)
	}
	status := limits[0].Status
	retry := "rate-limited — retry " + now.Add(10*time.Minute).Format("15:04")
	if count := strings.Count(status, "limits unavailable"); count > 1 {
		t.Fatalf("status %q says %q %d times, want at most once", status, "limits unavailable", count)
	}
	if !strings.HasPrefix(status, "account 6 "+retry) {
		t.Fatalf("status %q, want it to open with %q so a narrow pane keeps the retry time", status, "account 6 "+retry)
	}
}

func TestLimitsSamplerStaleRateLimitStatusPreservesRetryTime(t *testing.T) {
	// The backoff message this build writes, and the one an older build wrote
	// into a record that may still be active: both put the retry time first.
	for name, retryMessage := range map[string]string{
		"current": "rate-limited — retry 15:04 (429 Too Many Requests)",
		"older":   "limits unavailable: 429 Too Many Requests — retry at 15:04",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(paths.EnvHome, home)
			now := time.Unix(1_800_000_000, 0)
			account := LimitAccount{
				ID:        9,
				Engine:    pfmengine.Claude,
				Label:     "account 9",
				ConfigDir: filepath.Join(home, "claude"),
			}
			confirmedAt := now.Add(-10 * time.Minute)
			usage := liveClaudeUsage(confirmedAt, 49)
			if err := usagehook.WriteCacheRecord(
				usagehook.CachePath(usagehook.DefaultCacheDir(), account.ID),
				usagehook.CacheRecord{
					Usage:     usage,
					ConfigDir: account.ConfigDir,
					FetchedAt: &confirmedAt,
					Backoff: &usagehook.CacheBackoff{
						Message:    retryMessage,
						RetryAfter: now.Add(10 * time.Minute),
						RecordedAt: now,
					},
				},
			); err != nil {
				t.Fatal(err)
			}

			sampler := NewLimitsSampler([]LimitAccount{account})
			sampler.Now = func() time.Time { return now }
			limits, warnings := sampler.Sample(context.Background())
			if len(limits) != 1 || len(limits[0].Windows) != 2 ||
				limits[0].Status != "rate-limited — retry 15:04; showing cached limits" {
				t.Fatalf("rate-limited stale card=%#v, want retry time and cached windows", limits)
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], "15:04") {
				t.Fatalf("rate-limit warnings=%v, want retry time preserved", warnings)
			}
		})
	}
}

func TestHTTPStatusClassifiersMatchAStatusCodeNotADigitRun(t *testing.T) {
	const missingCredentials = "account 2 limits unavailable: read usage credentials: no /tmp/Sample1451742939/001/403/.cc/2/.credentials.json"
	for _, test := range []struct {
		name       string
		message    string
		auth       bool
		wantStatus string
		wantOK     bool
	}{
		{name: "429 in path", message: missingCredentials},
		{name: "429 in port", message: "dial tcp 127.0.0.1:44297: connect: connection refused"},
		{name: "429 in longer status", message: "status 4290"},
		{name: "current backoff", message: "rate-limited — retry 15:04 (429 Too Many Requests)", wantStatus: "rate-limited — retry 15:04", wantOK: true},
		{name: "older backoff", message: "limits unavailable: 429 Too Many Requests — retry at 15:04", wantStatus: "rate-limited — retry 15:04", wantOK: true},
		{name: "returned 429", message: "usage endpoint returned 429 Too Many Requests", wantStatus: "provider rate-limited", wantOK: true},
		{name: "http 429", message: "fetch Codex usage failed: HTTP 429", wantStatus: "provider rate-limited", wantOK: true},
		{name: "phrase only", message: "too many requests", wantStatus: "provider rate-limited", wantOK: true},
		{name: "returned 401", message: "usage endpoint returned 401", auth: true, wantOK: true},
		{name: "status 403", message: "status 403", auth: true, wantOK: true},
		{name: "403 in path", message: missingCredentials, auth: true},
		{name: "403 in longer status", message: "status 4031", auth: true},
		{name: "401 unauthorized", message: "401 Unauthorized", auth: true, wantOK: true},
		{name: "403 forbidden", message: "403 Forbidden", auth: true, wantOK: true},
		{name: "later valid 429", message: "status 4290, then status 429", wantStatus: "provider rate-limited", wantOK: true},
		{name: "later valid 403", message: "status 4031, then returned 403", auth: true, wantOK: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := errors.New(test.message)
			if test.auth {
				if got := isCredentialRejection(err); got != test.wantOK {
					t.Errorf("isCredentialRejection(%q) = %t, want %t", test.message, got, test.wantOK)
				}
				return
			}
			status, ok := rateLimitedStatus(err)
			if status != test.wantStatus || ok != test.wantOK {
				t.Errorf("rateLimitedStatus(%q) = (%q, %t), want (%q, %t)", test.message, status, ok, test.wantStatus, test.wantOK)
			}
		})
	}
}
