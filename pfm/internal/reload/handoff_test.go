package reload

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
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
		Engine: pfmengine.Claude, SessionID: "new-session", LeftBehind: "old-session", Account: 2,
		Cache1H: true, CWD: t.TempDir(), WrittenAt: time.Unix(42, 0),
	}
	if err := writeHandoff(lock, want); err != nil {
		t.Fatal(err)
	}
	got, exists, err := readHandoff(lock)
	if err != nil || !exists || got.Engine != want.Engine || got.SessionID != want.SessionID ||
		got.LeftBehind != want.LeftBehind ||
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
	legacy := `{"engine":"cx","session_id":"","account":2,"cache_1h":false,"cwd":"` + t.TempDir() + `","written_at":"1970-01-01T00:00:42Z"}`
	if err := lock.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := lock.WriteString(legacy); err != nil {
		t.Fatal(err)
	}
	got, exists, err = readHandoff(lock)
	if err != nil || !exists || got.LeftBehind != "" {
		t.Fatalf("legacy read=%+v exists=%t err=%v", got, exists, err)
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

func TestAdoptHandoffContinuesBoundCodexConversationAfterNew(t *testing.T) {
	const (
		oldID = "11111111-1111-4111-8111-111111111111"
		newID = "22222222-2222-4222-8222-222222222222"
	)
	for _, tc := range []struct {
		name       string
		leftBehind string
		id         string
		transcript bool
		newSeat    bool
		explicit   bool
		wantRefuse bool
	}{
		{name: "bound after new", leftBehind: oldID, id: newID, transcript: true},
		{name: "explicit account and cache", leftBehind: oldID, id: newID, transcript: true, explicit: true},
		{name: "resolved before new", leftBehind: oldID, id: oldID, transcript: true, wantRefuse: true},
		{name: "no resolved conversation", leftBehind: oldID, wantRefuse: true},
		{name: "no resolved transcript", leftBehind: oldID, id: newID, wantRefuse: true},
		{name: "new left nothing", id: newID, transcript: true, wantRefuse: true},
		{name: "legacy record", id: newID, transcript: true, wantRefuse: true},
		{name: "another new reload", leftBehind: oldID, id: newID, transcript: true, newSeat: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cwd := t.TempDir()
			record := handoffRecord{
				Engine: pfmengine.Codex, Account: 2, Cache1H: true,
				CWD: cwd, WrittenAt: time.Unix(2, 0),
			}
			content, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(content, &fields); err != nil {
				t.Fatal(err)
			}
			if tc.name != "legacy record" {
				fields["left_behind"] = tc.leftBehind
			}
			content, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(content, &record); err != nil {
				t.Fatal(err)
			}
			transcript := ""
			if tc.transcript {
				transcript = filepath.Join(dir, "rollout.jsonl")
				if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			request := Request{
				Engine: pfmengine.Codex, SocketPath: "/tmp/cx-1", Pane: "%7",
				SessionID: tc.id, Transcript: transcript, New: tc.newSeat,
				Account: 1, AccountIDs: []int{1, 2}, CWD: dir,
				AccountGiven: tc.explicit, CacheGiven: tc.explicit,
				Machine: pfmconfig.Config{CodexAccounts: []pfmconfig.CodexAccount{
					{ID: 1, Home: dir}, {ID: 2, Home: cwd},
				}},
			}
			got, continued, adopted, err := adoptHandoff(request, record, time.Unix(1, 0), dir)
			if tc.wantRefuse {
				if err == nil || !strings.Contains(err.Error(), "new Codex conversation whose id is not known yet") ||
					adopted {
					t.Fatalf("refusal adopted=%t err=%v", adopted, err)
				}
				return
			}
			wantAccount, wantCache := 2, true
			if tc.explicit {
				wantAccount, wantCache = 1, false
			}
			if err != nil || !adopted || got.SessionID != tc.id || got.Transcript != transcript ||
				got.Account != wantAccount || got.Cache1H != wantCache || got.CWD != cwd {
				t.Fatalf("adopted=%t request=%+v err=%v", adopted, got, err)
			}
			if !tc.newSeat && continued != tc.id {
				t.Fatalf("continued=%q, want %q", continued, tc.id)
			}
		})
	}
}
