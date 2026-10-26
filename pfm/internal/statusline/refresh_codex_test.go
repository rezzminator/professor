package statusline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestCodexRefresherWritesRecordedRateLimitsAtomically(t *testing.T) {
	root := t.TempDir()
	cachePath := filepath.Join(root, "gpt.json")
	if err := os.WriteFile(strings.TrimSuffix(cachePath, ".json")+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := mustFixture(t, "gpt-app-server.jsonl")
	err := RefreshCodex(context.Background(), CodexOptions{
		CachePath: cachePath,
		ReadRateLimits: func(context.Context) ([]byte, error) {
			return fixture, nil
		},
		Now: func() time.Time { return time.Unix(1_786_838_400, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var cached map[string]any
	if err := json.Unmarshal(body, &cached); err != nil {
		t.Fatal(err)
	}
	if cached["planType"] != "plus" || int(cached["ts"].(float64)) != 1_786_838_400 {
		t.Fatalf("gpt cache = %#v", cached)
	}
	if _, err := os.Stat(strings.TrimSuffix(cachePath, ".json") + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("GPT refresh lock survived completion: %v", err)
	}
}

func TestRenderSchedulesStaleRefreshersWithoutWaiting(t *testing.T) {
	root := t.TempDir()
	spawned := make(chan RefreshKind, 2)
	started := time.Now()
	_, err := Render(context.Background(), []byte(`{"model":{"display_name":"Claude"}}`), Runtime{
		Now:          func() time.Time { return time.Unix(1_786_838_400, 0) },
		Home:         root,
		ConfigDir:    filepath.Join(root, ".cc", "4"),
		CacheDir:     filepath.Join(root, "cache"),
		RateLimitDir: filepath.Join(root, "rates"),
		TmuxDir:      filepath.Join(root, "tmux"),
		ProcRoot:     filepath.Join(root, "proc"),
		UID:          1000,
		Engine:       pfmengine.Codex,
		Env:          map[string]string{},
		Command:      quietRunner{},
		Spawn: func(kind RefreshKind) error {
			spawned <- kind
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("render blocked for %v", elapsed)
	}
	select {
	case kind := <-spawned:
		if kind != RefreshKindCodex {
			t.Fatalf("spawned retired refresher %q, want only %q", kind, RefreshKindCodex)
		}
	default:
		t.Fatal("GPT refresher was not spawned")
	}
	select {
	case kind := <-spawned:
		t.Fatalf("spawned an extra refresher %q", kind)
	default:
	}
	_, err = Render(context.Background(), []byte(`{"model":{"display_name":"Claude"}}`), Runtime{
		Now:          func() time.Time { return time.Unix(1_786_838_400, 0) },
		Home:         root,
		ConfigDir:    filepath.Join(root, ".cc", "4"),
		CacheDir:     filepath.Join(root, "cache"),
		RateLimitDir: filepath.Join(root, "rates"),
		TmuxDir:      filepath.Join(root, "tmux"),
		ProcRoot:     filepath.Join(root, "proc"),
		UID:          1000,
		Engine:       pfmengine.Codex,
		Env:          map[string]string{},
		Command:      quietRunner{},
		Spawn: func(kind RefreshKind) error {
			spawned <- kind
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case kind := <-spawned:
		t.Fatalf("lock failed to suppress duplicate %s refresher", kind)
	default:
	}
}

func mustFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// A refresher that dies before its deferred unlock leaves the lock behind; one
// older than the refresh interval no longer suppresses the next refresh, while
// a live refresher's fresh lock still does.
func TestArmRefreshClearsALockOlderThanTheInterval(t *testing.T) {
	now := time.Unix(1_786_838_400, 0)
	const ttl = 5 * time.Minute
	for _, testcase := range []struct {
		name      string
		lockAge   time.Duration
		wantSpawn bool
	}{
		{name: "orphaned lock from a dead refresher", lockAge: 24 * time.Hour, wantSpawn: true},
		{name: "a running refresher's lock", lockAge: 10 * time.Second, wantSpawn: false},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			cachePath := filepath.Join(t.TempDir(), "cc-gpt-usage-1000.json")
			lockPath := strings.TrimSuffix(cachePath, ".json") + ".lock"
			if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			stamp := now.Add(-testcase.lockAge)
			if err := os.Chtimes(lockPath, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			spawned := 0
			armRefresh(Runtime{
				Now:   func() time.Time { return now },
				Spawn: func(RefreshKind) error { spawned++; return nil },
			}, RefreshKindCodex, cachePath, ttl)
			if got := spawned == 1; got != testcase.wantSpawn {
				t.Fatalf("spawned %d refresher(s), want spawn=%v", spawned, testcase.wantSpawn)
			}
			if _, err := os.Stat(lockPath); err != nil {
				t.Fatalf("lock after arming: %v, want one held for the refresher", err)
			}
		})
	}
}

// Two renders can see one orphaned lock at once; the one holding the guard
// clears it and takes the lock, so the other neither removes the lock that
// render just took nor spawns a second refresher.
func TestArmRefreshLeavesAnOrphanedLockToTheRenderClearingIt(t *testing.T) {
	now := time.Unix(1_786_838_400, 0)
	cachePath := filepath.Join(t.TempDir(), "cc-gpt-usage-1000.json")
	lockPath := strings.TrimSuffix(cachePath, ".json") + ".lock"
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := now.Add(-24 * time.Hour)
	if err := os.Chtimes(lockPath, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	guard, err := os.OpenFile(lockPath+".guard", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := guard.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := syscall.Flock(int(guard.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	spawned := 0
	armRefresh(Runtime{
		Now:   func() time.Time { return now },
		Spawn: func(RefreshKind) error { spawned++; return nil },
	}, RefreshKindCodex, cachePath, 5*time.Minute)
	if spawned != 0 {
		t.Fatalf("spawned %d refresher(s) while another render held the guard, want 0", spawned)
	}
	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("lock after arming: %v, want the orphan left to the guard holder", err)
	}
	if !info.ModTime().Equal(stamp) {
		t.Fatalf("lock mtime %v, want the orphan's %v untouched", info.ModTime(), stamp)
	}
}
