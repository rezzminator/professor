package codexmeta

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWalkRolloutsListsEachSubtreeInOrderAndSkipsTheHome(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{
		"sessions/2026/09/21/rollout-2026-09-21T08-00-00-b.jsonl",
		"sessions/2026/09/20/rollout-2026-09-20T08-00-00-a.jsonl",
		"sessions/2026/09/20/notes.jsonl",
		"archived_sessions/rollout-2026-09-01T08-00-00-old.jsonl",
		"rollout-backup-2026-09-01T08-00-00-old.jsonl",
	} {
		path := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(home, "sessions", "rollout-2026-09-22T08-00-00-link.jsonl")
	if err := os.Symlink(
		filepath.Join(home, "archived_sessions", "rollout-2026-09-01T08-00-00-old.jsonl"),
		link,
	); err != nil {
		t.Fatal(err)
	}
	rollouts, errs := WalkRollouts(context.Background(), home, SessionsDir, ArchivedSessionsDir, "absent")
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, rollout := range rollouts {
		rel, err := filepath.Rel(resolved, rollout.Path)
		if err != nil {
			t.Fatal(err)
		}
		if rollout.Size != 3 || rollout.ModTime.IsZero() {
			t.Fatalf("%s: size %d mtime %v", rel, rollout.Size, rollout.ModTime)
		}
		got = append(got, rel)
	}
	want := []string{
		"sessions/2026/09/20/rollout-2026-09-20T08-00-00-a.jsonl",
		"sessions/2026/09/21/rollout-2026-09-21T08-00-00-b.jsonl",
		"archived_sessions/rollout-2026-09-01T08-00-00-old.jsonl",
	}
	if len(got) != len(want) {
		t.Fatalf("walk = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != filepath.FromSlash(want[i]) {
			t.Fatalf("walk = %q, want %q", got, want)
		}
	}
}

func TestWalkRolloutsReportsAnUnreadableSubtreeAndKeepsTheRest(t *testing.T) {
	home := t.TempDir()
	// sessions is a link to itself: resolving it fails for any user, root included.
	if err := os.Symlink("sessions", filepath.Join(home, "sessions")); err != nil {
		t.Fatal(err)
	}
	archived := filepath.Join(home, "archived_sessions")
	if err := os.MkdirAll(archived, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archived, "rollout-2026-09-20T08-00-00-x.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	rollouts, errs := WalkRollouts(context.Background(), home, SessionsDir, ArchivedSessionsDir)
	if len(rollouts) != 1 || filepath.Base(filepath.Dir(rollouts[0].Path)) != ArchivedSessionsDir {
		t.Fatalf("rollouts = %+v, want archived_sessions' one", rollouts)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "sessions") {
		t.Fatalf("errs = %v, want the looping sessions root named once", errs)
	}
}

func TestParseRolloutName(t *testing.T) {
	plusTwo := time.FixedZone("UTC+2", 2*60*60)
	for _, tc := range []struct {
		name, id    string
		started     time.Time
		stamped, ok bool
	}{
		{"rollout-2026-09-20T08-30-05-019a-thread.jsonl", "019a-thread", time.Date(2026, 9, 20, 6, 30, 5, 0, time.UTC), true, true},
		{"rollout-2026-13-20T08-30-05-x.jsonl", "x", time.Time{}, false, true},
		{"rollout-unusual.jsonl", "", time.Time{}, false, false},
		{"rollout-2026-09-20T08-30-05-.jsonl", "", time.Time{}, false, false},
	} {
		id, started, stamped, ok := ParseRolloutName(tc.name, plusTwo)
		if id != tc.id || ok != tc.ok || stamped != tc.stamped || (stamped && !started.Equal(tc.started)) {
			t.Fatalf("ParseRolloutName(%q) = %q %v %v %v; want %q %v %v %v",
				tc.name, id, started, stamped, ok, tc.id, tc.started, tc.stamped, tc.ok)
		}
	}
}

func TestRolloutID(t *testing.T) {
	for name, want := range map[string]string{
		"rollout-2026-01-02T03-04-05-id.jsonl": "id",
		"rollout-unusual.jsonl":                "unusual",
	} {
		if got := RolloutID(name); got != want {
			t.Fatalf("RolloutID(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestAgentPathRolloutsMatchesEitherAgentPathForm(t *testing.T) {
	home, extra := t.TempDir(), t.TempDir()
	write := func(path, content string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	direct := write(filepath.Join(home, SessionsDir, "2026", "09", "29", "rollout-a.jsonl"),
		`{"type":"session_meta","payload":{"id":"a","agent_path":"/root/fix_1a"}}`+"\n")
	spawned := write(
		filepath.Join(home, ArchivedSessionsDir, "rollout-b.jsonl"),
		`{"type":"session_meta","payload":{"id":"b","source":{"subagent":{"thread_spawn":{"agent_path":"/root/fix_1a"}}}}}`+"\n",
	)
	broken := write(filepath.Join(extra, "deep", "rollout-c.jsonl"), "{not json\n")
	write(
		filepath.Join(extra, "rollout-d.jsonl"),
		`{"type":"session_meta","payload":{"id":"d","agent_path":"/root/other"}}`+"\n",
	)
	hits, unreadable, errs := AgentPathRollouts(context.Background(), []string{home}, []string{extra}, "/root/fix_1a")
	if len(hits) != 2 || hits[0] != direct || hits[1] != spawned || len(errs) != 0 {
		t.Errorf("AgentPathRollouts hits = %q, errs = %v; want %q, %q", hits, errs, direct, spawned)
	}
	if len(unreadable) != 1 || unreadable[0].Path != broken || unreadable[0].Err == nil {
		t.Errorf("unreadable = %+v, want %s with its error", unreadable, broken)
	}
}
