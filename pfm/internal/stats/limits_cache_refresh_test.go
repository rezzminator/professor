package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/usagehook"
)

// Both adapters use real HTTP and real shared-cache files, with invented
// credentials and a jailed home. No provider override bypasses the cache.
func TestLimitsSharedCacheRefreshBoundaries(t *testing.T) {
	for _, engine := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex} {
		for _, scenario := range []string{"confirmation-age", "future", "empty"} {
			t.Run(string(engine)+"/"+scenario, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv(paths.EnvHome, home)
				now := time.Unix(1_800_000_000, 0)
				// Just short of the live TTL, so the first Sample must read
				// through the shared cache and the second, past it, refetches.
				confirmed := now.Add(4*time.Second - LiveLimitsTTL)
				if scenario == "future" {
					confirmed = now.Add(time.Minute)
				}
				account := LimitAccount{
					ID:        21,
					Engine:    engine,
					Label:     "fixture account",
					ConfigDir: filepath.Join(home, "claude"),
				}
				writeFixtureCredentials(t, account.ConfigDir)
				account.CodexAuthPath = writeCodexAuth(t, home, "fixture-access", "fixture-account")
				claudeUsage := liveClaudeUsage(now, 54)
				codexPayload := liveCodexUsage(now, 54)
				if scenario == "empty" {
					claudeUsage, codexPayload = usagehook.Usage{}, codexUsage{}
				}
				var err error
				if engine == pfmengine.Claude {
					err = usagehook.WriteCacheRecord(
						usagehook.CachePath(usagehook.DefaultCacheDir(), account.ID),
						usagehook.CacheRecord{
							Usage: claudeUsage, ConfigDir: account.ConfigDir, FetchedAt: &confirmed,
						},
					)
				} else {
					err = writeCodexCacheRecord(
						codexCachePath(usagehook.DefaultCacheDir(), account.ID),
						codexCacheRecord{
							codexUsage: codexPayload, SourceVersion: codexUsageSourceVersion,
							CodexAuthPath: account.CodexAuthPath, FetchedAt: &confirmed,
						},
					)
				}
				if err != nil {
					t.Fatal(err)
				}
				var hits atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					hits.Add(1)
					w.Header().Set("Content-Type", "application/json")
					if engine == pfmengine.Claude {
						fmt.Fprint(w, usageJSONBody(46, 46, confirmed.Add(time.Minute)))
					} else if err := json.NewEncoder(w).Encode(liveCodexUsage(confirmed.Add(time.Minute), 46)); err != nil {
						t.Errorf("encode fixture usage: %v", err)
					}
				}))
				defer server.Close()
				sampler := NewLimitsSampler([]LimitAccount{account})
				sampler.TTL = LiveLimitsTTL
				sampler.Now = func() time.Time { return now }
				sampler.Endpoint, sampler.CodexEndpoint = server.URL, server.URL
				sampler.Client, sampler.CodexClient = server.Client(), server.Client()
				limits, warnings := sampler.Sample(context.Background())
				if scenario == "confirmation-age" {
					if hits.Load() != 0 || len(warnings) != 0 || limits[0].Windows[0].UsedPct != 54 {
						t.Fatalf("fresh shared cache: hits=%d limits=%#v warnings=%v", hits.Load(), limits, warnings)
					}
					now = now.Add(6 * time.Second)
					limits, warnings = sampler.Sample(context.Background())
				}
				if hits.Load() != 1 || len(warnings) != 0 || len(limits[0].Windows) == 0 ||
					limits[0].Windows[0].UsedPct != 46 {
					t.Fatalf(
						"cache prevented required refresh: hits=%d limits=%#v warnings=%v",
						hits.Load(),
						limits,
						warnings,
					)
				}
			})
		}
	}
}

func TestLimitsCanceledHTTPDoesNotPoisonSharedCache(t *testing.T) {
	for _, engine := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex} {
		t.Run(string(engine), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(paths.EnvHome, home)
			now := time.Unix(1_800_000_000, 0)
			account := LimitAccount{
				ID:        22,
				Engine:    engine,
				Label:     "fixture account",
				ConfigDir: filepath.Join(home, "claude"),
			}
			writeFixtureCredentials(t, account.ConfigDir)
			account.CodexAuthPath = writeCodexAuth(t, home, "fixture-access", "fixture-account")
			started := make(chan struct{}, 1)
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if hits.Add(1) == 1 {
					started <- struct{}{}
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if engine == pfmengine.Claude {
					fmt.Fprint(w, usageJSONBody(46, 46, now))
				} else if err := json.NewEncoder(w).Encode(liveCodexUsage(now, 46)); err != nil {
					t.Errorf("encode fixture usage: %v", err)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sampler := NewLimitsSampler([]LimitAccount{account})
			sampler.TTL = LiveLimitsTTL
			sampler.Now = func() time.Time { return now }
			sampler.Endpoint, sampler.CodexEndpoint = server.URL, server.URL
			sampler.Client, sampler.CodexClient = server.Client(), server.Client()
			sampler.SampleLive(ctx)
			waitForLimitSignal(t, started)
			cancel()
			waitForNoCachedEntry(t, sampler, account.ID)
			cachePath := usagehook.CachePath(usagehook.DefaultCacheDir(), account.ID)
			if engine == pfmengine.Codex {
				cachePath = codexCachePath(usagehook.DefaultCacheDir(), account.ID)
			}
			if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
				t.Fatalf("canceled HTTP wrote shared cache/backoff: stat error=%v", err)
			}
			sampler.SampleLive(context.Background())
			waitForCachedWindow(t, sampler, account.ID, 46)
			if hits.Load() != 2 {
				t.Fatalf("requests=%d, want canceled request plus immediate retry", hits.Load())
			}
		})
	}
}

func liveCodexUsage(now time.Time, used float64) codexUsage {
	return codexUsage{RateLimit: &codexRateLimitBucket{PrimaryWindow: &codexRateLimitWindow{
		UsedPercent: used, ResetAt: now.Add(5 * time.Hour).Unix(), LimitWindowSeconds: 18000,
	}}}
}
