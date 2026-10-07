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
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// doorSeat is one signed-in account whose shared cache holds a matching record
// fetched ten minutes before now: expired for every caller's TTL, still inside
// the one-hour stale horizon. The jail env points the statusline snapshot
// directory inside root.
func doorSeat(t *testing.T, account int, now time.Time) (root, configDir, cacheDir string, env paths.Env) {
	t.Helper()
	root, configDir, cacheDir = adversarialSeat(t, account)
	fetchedAt := now.Add(-10 * time.Minute)
	if err := WriteCacheRecord(CachePath(cacheDir, account), CacheRecord{
		Usage: Usage{
			FiveHour: Window{Utilization: usageFloatPtr(20), ResetsAt: now.Add(2 * time.Hour).Format(time.RFC3339)},
			SevenDay: Window{Utilization: usageFloatPtr(20), ResetsAt: now.Add(48 * time.Hour).Format(time.RFC3339)},
		},
		ConfigDir: configDir, FetchedAt: &fetchedAt,
	}); err != nil {
		t.Fatal(err)
	}
	return root, configDir, cacheDir, &paths.MapEnv{Values: map[string]string{paths.EnvHome: root}}
}

// countingUsageServer answers every request with a 10%/5% payload after
// delay, counting requests and keeping the last User-Agent it saw.
func countingUsageServer(
	t *testing.T,
	now time.Time,
	delay time.Duration,
) (*httptest.Server, *atomic.Int64, func() string) {
	t.Helper()
	var hits atomic.Int64
	var mu sync.Mutex
	agent := ""
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		mu.Lock()
		agent = request.Header.Get("User-Agent")
		mu.Unlock()
		time.Sleep(delay)
		_, _ = io.WriteString(writer, `{"five_hour":{"utilization":10,"resets_at":"`+
			now.Add(2*time.Hour).UTC().Format(time.RFC3339)+`"},"seven_day":{"utilization":5,"resets_at":"`+
			now.Add(48*time.Hour).UTC().Format(time.RFC3339)+`"}}`)
	}))
	t.Cleanup(server.Close)
	return server, &hits, func() string { mu.Lock(); defer mu.Unlock(); return agent }
}

// T1: a 429 the hook receives becomes the shared backoff every reader honors,
// with the server's Retry-After when it sent one and a ten-minute floor when not.
func TestEvaluateRecordsA429AsTheSharedBackoff(t *testing.T) {
	for _, testcase := range []struct {
		name       string
		retryAfter string
		atLeast    time.Duration
		atMost     time.Duration
	}{
		{name: "retry-after 3600", retryAfter: "3600", atLeast: time.Hour - 5*time.Second, atMost: time.Hour + 5*time.Second},
		{name: "no retry-after", atLeast: 10 * time.Minute, atMost: 11 * time.Minute},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			root, configDir, cacheDir, env := doorSeat(t, 5, now)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if testcase.retryAfter != "" {
					writer.Header().Set("Retry-After", testcase.retryAfter)
				}
				writer.WriteHeader(http.StatusTooManyRequests)
			}))
			defer server.Close()
			var log bytes.Buffer
			if _, err := Evaluate(context.Background(), Options{
				Now: func() time.Time { return now }, Env: env, Home: root, ConfigDir: configDir,
				AccountDirs: map[string]int{configDir: 5}, CacheDir: cacheDir, TTL: 3 * time.Minute,
				Client: server.Client(), Endpoint: server.URL, Log: &log,
			}); err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			record, err := ReadCacheRecord(CachePath(cacheDir, 5))
			if err != nil {
				t.Fatalf("read cache after 429: %v", err)
			}
			if record.Backoff == nil {
				t.Fatalf("429 wrote no shared backoff: record=%+v log=%q", record, log.String())
			}
			wait := record.Backoff.RetryAfter.Sub(now)
			if wait < testcase.atLeast || wait > testcase.atMost {
				t.Fatalf("backoff retry_after = now+%s, want within [%s, %s]", wait, testcase.atLeast, testcase.atMost)
			}
			if record.FiveHour.Utilization == nil || *record.FiveHour.Utilization != 20 {
				t.Fatalf("429 backoff dropped the last-good usage: %+v", record.Usage)
			}
			if !strings.Contains(log.String(), "429") {
				t.Fatalf("hook log never named the failed refresh: %q", log.String())
			}
		})
	}
}

// T2: two processes' worth of callers against an expired cache spend exactly
// one request between them.
func TestConcurrentCallersSpendOneRequest(t *testing.T) {
	now := time.Now()
	root, configDir, cacheDir, env := doorSeat(t, 6, now)
	server, hits, _ := countingUsageServer(t, now, 300*time.Millisecond)
	var group sync.WaitGroup
	errs := make([]error, 2)
	for index := range errs {
		group.Add(1)
		go func() {
			defer group.Done()
			_, errs[index] = Evaluate(context.Background(), Options{
				Env: env, Home: root, ConfigDir: configDir,
				AccountDirs: map[string]int{configDir: 6}, CacheDir: cacheDir, TTL: 3 * time.Minute,
				Client: server.Client(), Endpoint: server.URL, Log: io.Discard,
			})
		}()
	}
	group.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", index, err)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("two concurrent callers sent %d requests, want exactly 1", got)
	}
}

// T3: a fresh statusline snapshot for this seat answers without a request,
// and its windows are the ones the caller sees.
func TestFreshStatuslineSnapshotAnswersWithoutARequest(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	root, configDir, cacheDir, env := doorSeat(t, 7, now)
	snapshotDir := filepath.Join(root, "tmp", "cc-rate-limits")
	if err := os.MkdirAll(snapshotDir, 0o700); err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(map[string]any{
		"acct": 7, "config_dir": configDir, "ts": now.Add(-5 * time.Second).Unix(),
		"five_hour_used": 85, "five_hour_resets_at": now.Add(2 * time.Hour).Unix(),
		"seven_day_used": 30, "seven_day_resets_at": now.Add(48 * time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshotDir, "acct-7.session-a.json"), snapshot, 0o600); err != nil {
		t.Fatal(err)
	}
	server, hits, _ := countingUsageServer(t, now, 0)
	message, err := Evaluate(context.Background(), Options{
		Now: func() time.Time { return now }, Env: env, Home: root, ConfigDir: configDir,
		AccountDirs: map[string]int{configDir: 7}, CacheDir: cacheDir, Warn: 80, Critical: 95, TTL: 3 * time.Minute,
		Client: server.Client(), Endpoint: server.URL, Log: io.Discard,
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("fresh snapshot still sent %d requests, want 0", got)
	}
	if !strings.Contains(message, "5h 85%") || !strings.Contains(message, "7d 30%") {
		t.Fatalf("hook did not answer from the snapshot's windows: %q", message)
	}
}

// T4: the cross-process lock. A live lock held by a peer sends no request and
// is left alone; a lock older than thirty seconds is a crashed peer's and never
// blocks a fetch.
func TestRefreshLockBlocksAPeerButNotAStaleLock(t *testing.T) {
	for _, testcase := range []struct {
		name     string
		age      time.Duration
		wantHits int64
		wantLock bool
	}{
		{name: "live peer lock", age: 0, wantHits: 0, wantLock: true},
		{name: "lock one second short of stale", age: 29 * time.Second, wantHits: 0, wantLock: true},
		{name: "lock one second past stale", age: 31 * time.Second, wantHits: 1, wantLock: false},
		{name: "stale lock", age: 60 * time.Second, wantHits: 1, wantLock: false},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			now := time.Now()
			root, configDir, cacheDir, env := doorSeat(t, 8, now)
			lockPath := filepath.Join(cacheDir, "acct-8.lock")
			if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			stamp := now.Add(-testcase.age)
			if err := os.Chtimes(lockPath, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			server, hits, _ := countingUsageServer(t, now, 0)
			if _, err := Evaluate(context.Background(), Options{
				Env: env, Home: root, ConfigDir: configDir,
				AccountDirs: map[string]int{configDir: 8}, CacheDir: cacheDir, TTL: 3 * time.Minute,
				Client: server.Client(), Endpoint: server.URL, Log: io.Discard,
			}); err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if got := hits.Load(); got != testcase.wantHits {
				t.Fatalf("requests = %d, want %d", got, testcase.wantHits)
			}
			_, statErr := os.Stat(lockPath)
			if held := statErr == nil; held != testcase.wantLock {
				t.Fatalf("lock file present=%v after the call, want %v (stat: %v)", held, testcase.wantLock, statErr)
			}
		})
	}
}

// T5: the request names pfm and the running version, never a borrowed client
// identity; an unstamped build names itself dev.
func TestUsageRequestCarriesThePfmUserAgent(t *testing.T) {
	for _, testcase := range []struct{ version, want string }{
		{version: "", want: "pfm/dev"},
		{version: "9.9.9", want: "pfm/9.9.9"},
	} {
		t.Run(testcase.want, func(t *testing.T) {
			now := time.Now()
			root, configDir, cacheDir, env := doorSeat(t, 9, now)
			server, hits, agent := countingUsageServer(t, now, 0)
			if _, err := Evaluate(context.Background(), Options{
				Env: env, Home: root, ConfigDir: configDir,
				AccountDirs: map[string]int{configDir: 9}, CacheDir: cacheDir, TTL: 3 * time.Minute,
				Client: server.Client(), Endpoint: server.URL, Log: io.Discard, Version: testcase.version,
			}); err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if hits.Load() != 1 {
				t.Fatalf("requests = %d, want 1", hits.Load())
			}
			if got := agent(); got != testcase.want {
				t.Fatalf("User-Agent = %q, want %q", got, testcase.want)
			}
		})
	}
}

// The door and the Limits tab share one "still worth showing" rule: a window
// counts only with a reading and a reset not yet passed, an unknown reset
// being no passed one.
func TestHasCurrentWindowNeedsAReadingAndAResetNotYetPassed(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	future, past := now.Add(time.Hour).Format(time.RFC3339), now.Add(-time.Minute).Format(time.RFC3339)
	for _, testcase := range []struct {
		name  string
		usage Usage
		want  bool
	}{
		{"reading with a future reset", Usage{SevenDay: Window{Utilization: usageFloatPtr(40), ResetsAt: future}}, true},
		{"reading with an unknown reset", Usage{FiveHour: Window{Utilization: usageFloatPtr(40)}}, true},
		{"reading past its reset", Usage{FiveHour: Window{Utilization: usageFloatPtr(40), ResetsAt: past}}, false},
		{"future reset without a reading", Usage{SevenDay: Window{ResetsAt: future}}, false},
		{"no window", Usage{}, false},
	} {
		if got := HasCurrentWindow(testcase.usage, now); got != testcase.want {
			t.Errorf("%s: HasCurrentWindow = %v, want %v", testcase.name, got, testcase.want)
		}
	}
}

// TestClaudeProbesNeverWriteTheRealClaudeHome: the dependency probe (version
// and self-doctor, as pfm doctor and pfm install run it) and the usage hook's
// version read each run a logged-out claude. Run with the ambient env, it
// recreated ~/.claude/backups and $HOME/.claude.json and the host check then
// blocked the machine; each run now gets a throwaway home that is gone after.
func TestClaudeProbesNeverWriteTheRealClaudeHome(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	sid := filepath.Join(t.TempDir(), "sid")
	t.Setenv(paths.EnvSIDDir, sid)
	scratch := t.TempDir()
	record := filepath.Join(scratch, "config-dirs.log")
	binary := filepath.Join(scratch, "claude")
	if err := testjail.WriteLoggedOutClaude(binary, record, ""); err != nil {
		t.Fatal(err)
	}
	var entries []deps.Entry
	registry := deps.Registry(deps.Options{Home: home, ClaudeBinary: binary})
	for index := range registry {
		if registry[index].Engine == pfmengine.Claude {
			entries = append(entries, registry[index])
		}
	}
	results := deps.Probe(context.Background(), entries, deps.ProbeOptions{GOOS: runtime.GOOS})
	if len(results) != 1 || results[0].State != deps.StateOK || results[0].SelfDoctor != "ok" {
		t.Fatalf("dependency probe = %#v, want one healthy claude", results)
	}
	version, err := claudeCodeVersion(context.Background(), binary)
	if err != nil || version != "2.1.238" {
		t.Fatalf("usage hook version = %q, %v", version, err)
	}
	if runs := testjail.AssertClaudeRanInThrowawayHomes(t, home, sid, record); runs != 4 {
		t.Errorf("the fake recorded %d runs, want 4 (--version, doctor --help, doctor, the usage --version)", runs)
	}
}
