package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
)

func TestRunRebuildsMessagesAndCompactionMemoryFromRollout(t *testing.T) {
	threadID := "11111111-1111-4111-8111-111111111111"
	large := strings.Repeat("x", 70*1024)
	content := strings.Join([]string{
		`{"type":"response_item","timestamp":"2026-08-17T10:00:00Z","payload":{"type":"message","role":"user","content":[{"text":"hello"}]}}`,
		`{"type":"response_item","timestamp":"2026-08-17T10:01:00Z","payload":{"type":"message","role":"assistant","content":[{"text":"` + large + `"}]}}`,
		`{"type":"compacted","timestamp":"2026-08-17T10:02:00Z","payload":{"replacement_history":[{"role":"assistant","content":[{"text":"carried forward"}]},{"role":"user","content":[{"text":"# AGENTS.md instructions are boilerplate"}]}]}}`,
		"not json",
	}, "\n") + "\n"
	stale := `{"type":"response_item","timestamp":"2026-08-17T09:00:00Z","payload":{"type":"message","role":"user","content":[{"text":"stale"}]}}` + "\n"
	write := func(t *testing.T, path, body string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	compress := func(t *testing.T, path string) string {
		t.Helper()
		compressed, err := codexmeta.CompressRollout(path)
		if err != nil {
			t.Fatal(err)
		}
		return compressed
	}
	day := filepath.Join("sessions", "2026", "08", "17")
	for _, testCase := range []struct {
		name  string
		build func(t *testing.T, root string) (target, rollout string)
	}{
		{"plain rollout by path", func(t *testing.T, root string) (string, string) {
			rollout := write(t, filepath.Join(root, "rollout-"+threadID+".jsonl"), content)
			return rollout, rollout
		}},
		{"compressed rollout by thread id", func(t *testing.T, root string) (string, string) {
			return threadID, compress(t, write(t, filepath.Join(root, day, "rollout-"+threadID+".jsonl"), content))
		}},
		{"plain twin of a newer compressed rollout", func(t *testing.T, root string) (string, string) {
			plain := filepath.Join(root, day, "rollout-"+threadID+".jsonl")
			compressed := compress(t, write(t, plain, stale))
			write(t, plain, content)
			newer := time.Now().Add(time.Hour)
			if err := os.Chtimes(compressed, newer, newer); err != nil {
				t.Fatal(err)
			}
			return threadID, plain
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			target, rollout := testCase.build(t, root)
			result, err := Run(context.Background(), root, target)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ThreadID != threadID || result.Rollout != rollout || result.Messages != 2 ||
				result.Carried != 1 ||
				result.Malformed != 1 {
				t.Fatalf("Run() result = %+v, want rollout %s with 2 messages, 1 carried, 1 malformed", result, rollout)
			}
			checkBundle(t, root, threadID, large)
		})
	}
}

// checkBundle reads the recovered bundle under root: every file private, the
// transcript holding both messages, the memory the carried one alone.
func checkBundle(t *testing.T, root, threadID, large string) {
	t.Helper()
	out := filepath.Join(root, "recovered-"+threadID)
	for _, name := range []string{"transcript.md", "compaction-memory.md", "brief.md"} {
		path := filepath.Join(out, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("missing recovered %s: %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("recovered %s mode = %o, want 0600", name, info.Mode().Perm())
		}
	}
	transcript, err := os.ReadFile(filepath.Join(out, "transcript.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(transcript), "hello") || !strings.Contains(string(transcript), large) {
		t.Fatal("transcript omitted a parsed message")
	}
	memory, err := os.ReadFile(filepath.Join(out, "compaction-memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(memory), "carried forward") || strings.Contains(string(memory), "AGENTS.md") {
		t.Fatalf("compaction memory filtering regressed:\n%s", memory)
	}
}

func TestRunChecksContextBeforeLocatingRollout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, t.TempDir(), "missing-thread")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}
