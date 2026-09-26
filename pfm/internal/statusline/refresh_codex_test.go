package statusline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
