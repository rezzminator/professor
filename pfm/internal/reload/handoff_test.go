package reload

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestRunRejectsUnreadableHandoffBeforeChangingPane(t *testing.T) {
	dir := t.TempDir()
	lockPath := LockPath(dir, "probe-1", "%7")
	if err := os.WriteFile(lockPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmux := &fakeReloadTmux{}
	_, err := Run(context.Background(), Request{
		Engine: pfmengine.Claude, SocketPath: "/tmp/probe-1", Pane: "%7",
		SessionID: "11111111-1111-4111-8111-111111111111", Account: 1, AccountIDs: []int{1},
	}, Options{SIDDir: dir, Delay: -1, Poll: -1}, tmux, nil, nil)
	if err == nil || !strings.Contains(err.Error(), lockPath) || !strings.Contains(err.Error(), "remove it") ||
		tmux.literal != "" {
		t.Fatalf("unreadable handoff error=%v literal=%q", err, tmux.literal)
	}
}

func TestHandoffRecordRoundTripsAndReadClearsIt(t *testing.T) {
	lock, err := os.CreateTemp(t.TempDir(), "lock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	want := handoffRecord{
		Engine: pfmengine.Claude, SessionID: "new-session", Account: 2,
		Cache1H: true, CWD: t.TempDir(), WrittenAt: time.Unix(42, 0),
	}
	if err := writeHandoff(lock, want); err != nil {
		t.Fatal(err)
	}
	got, exists, err := readHandoff(lock)
	if err != nil || !exists || got.Engine != want.Engine || got.SessionID != want.SessionID ||
		got.Account != want.Account || got.Cache1H != want.Cache1H || got.CWD != want.CWD ||
		!got.WrittenAt.Equal(want.WrittenAt) {
		t.Fatalf("read=%+v exists=%t err=%v, want %+v", got, exists, err, want)
	}
	if _, exists, err := readHandoff(lock); err != nil || exists {
		t.Fatalf("second read exists=%t err=%v", exists, err)
	}
	if err := writeHandoff(lock, want); err != nil {
		t.Fatal(err)
	}
	if info, err := lock.Stat(); err != nil || info.Size() == 0 {
		t.Fatalf("rewrite info=%v err=%v", info, err)
	}
}

func TestHandoffRejectsUnreadableRecord(t *testing.T) {
	for _, content := range []string{"{", "null", "{}"} {
		t.Run(content, func(t *testing.T) {
			lock, err := os.CreateTemp(t.TempDir(), "lock")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lock.Close() })
			if _, err := lock.WriteString(content); err != nil {
				t.Fatal(err)
			}
			if _, _, err := readHandoff(lock); err == nil {
				t.Fatalf("invalid record %q accepted", content)
			}
		})
	}
}

func TestAdoptHandoffUsesEntryAndBreadcrumbAndExplicitFlags(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(paths.EnvHome, t.TempDir())
	cwd := t.TempDir()
	entry := time.Unix(100, 0)
	record := handoffRecord{
		Engine: pfmengine.Claude, SessionID: "new-session", Account: 2,
		Cache1H: true, CWD: cwd, WrittenAt: entry.Add(time.Second),
	}
	request := Request{
		Engine: pfmengine.Claude, SocketPath: "/tmp/probe-1", Pane: "%7",
		SessionID: "old-session", Account: 1, Cache1H: false, CWD: dir,
		Machine: reloadTestMachine("", dir),
	}
	got, left, adopted, err := adoptHandoff(request, record, entry, dir)
	if err != nil || !adopted || left != record.SessionID || got.SessionID != record.SessionID ||
		got.Account != 2 || !got.Cache1H || got.CWD != cwd || !got.fresh {
		t.Fatalf("adopted=%t left=%q request=%+v err=%v", adopted, left, got, err)
	}
	request.AccountGiven, request.CacheGiven = true, true
	got, _, _, err = adoptHandoff(request, record, entry, dir)
	if err != nil || got.Account != 1 || got.Cache1H {
		t.Fatalf("explicit values request=%+v err=%v", got, err)
	}
	request.New = true
	got, left, _, err = adoptHandoff(request, record, entry, dir)
	if err != nil || got.SessionID != "old-session" || left != "new-session" || got.Account != 1 || got.CWD != cwd {
		t.Fatalf("new request=%+v left=%q err=%v", got, left, err)
	}
	crumb := filepath.Join(dir, "probe-1.%7")
	if err := os.WriteFile(crumb, []byte("newer"), 0o600); err != nil {
		t.Fatal(err)
	}
	newer := entry.Add(3 * time.Second)
	if err := os.Chtimes(crumb, newer, newer); err != nil {
		t.Fatal(err)
	}
	if _, _, adopted, err := adoptHandoff(request, record, entry.Add(2*time.Second), dir); err != nil || adopted {
		t.Fatalf("newer crumb adopted=%t err=%v", adopted, err)
	}
	if err := os.Chtimes(crumb, entry, entry); err != nil {
		t.Fatal(err)
	}
	if _, _, adopted, err := adoptHandoff(request, record, entry.Add(2*time.Second), dir); err != nil || !adopted {
		t.Fatalf("older crumb adopted=%t err=%v", adopted, err)
	}
}

func TestAdoptHandoffRefusesUnknownCodexConversation(t *testing.T) {
	request := Request{Engine: pfmengine.Codex, SocketPath: "/tmp/cx-1", Pane: "%7"}
	record := handoffRecord{Engine: pfmengine.Codex, WrittenAt: time.Unix(2, 0)}
	_, _, _, err := adoptHandoff(request, record, time.Unix(1, 0), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "new Codex conversation whose id is not known yet") {
		t.Fatalf("Codex error=%v", err)
	}
}
