package mockengine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/reload"
)

func TestClaudeTranscriptTitlePrecedenceAndErrors(t *testing.T) {
	fix := newFixture(t)
	path := fix.claudeTranscript(fixtureSession)
	if got, err := claudeTranscriptTitle(path); got != "" || err != nil {
		t.Fatalf("absent: %q %v", got, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("bad\n"+`{"type":"ai-title","aiTitle":"auto title"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := claudeTranscriptTitle(path); got != "auto title" || err != nil {
		t.Fatalf("ai: %q %v", got, err)
	}
	titles := `{"type":"custom-title","customTitle":"old"}` + "\n" +
		`{"type":"ai-title","aiTitle":"auto title"}` + "\n" +
		`{"type":"custom-title","customTitle":"fresh-name"}` + "\n"
	if err := os.WriteFile(path, []byte(titles), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := claudeTranscriptTitle(path); got != "fresh-name" || err != nil {
		t.Fatalf("custom: %q %v", got, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := claudeTranscriptTitle(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("unreadable: %v", err)
	}
}

func TestClaudeForkSeedRewritesIdentity(t *testing.T) {
	fix := newFixture(t)
	parent, child := fix.claudeTranscript(fixtureSession), fix.claudeTranscript("fork-id")
	if err := os.MkdirAll(filepath.Dir(parent), 0o700); err != nil {
		t.Fatal(err)
	}
	data := `{"type":"user","sessionId":"` + fixtureSession + `","message":{"role":"user","content":"hello"}}` + "\n" + `{"type":"custom-title","customTitle":"parent"}` + "\n"
	if err := os.WriteFile(parent, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedClaudeFork(parent, child, "fork-id"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, parent); got != data {
		t.Fatalf("parent changed: %s", got)
	}
	got := readFile(t, child)
	if strings.Contains(got, fixtureSession) || strings.Contains(got, "custom-title") ||
		!strings.Contains(got, `"sessionId":"fork-id"`) {
		t.Fatalf("child: %s", got)
	}
}

func TestClaudeForkOwnsTranscriptAndTitle(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{BusyMS: intPtr(0)})
	parent := fix.startTUI("claude", claudeArgs("--session-id", fixtureSession, "--name", "parent"), nil)
	parent.waitFrame("composer", func(s string) bool { return strings.Contains(s, "❯") })
	parent.typeLine("one")
	parent.waitFrame("reply", func(s string) bool { return strings.Contains(s, DefaultReply) })
	parent.typeLine("/exit")
	if code := parent.waitExit(); code != 0 {
		t.Fatalf("parent exit %d", code)
	}
	before, err := os.ReadFile(fix.claudeTranscript(fixtureSession))
	if err != nil {
		t.Fatal(err)
	}
	const forkID = "f2222222-2222-4222-8222-222222222222"
	forkArgs := claudeArgs("--resume", fixtureSession, "--fork-session", "--session-id", forkID, "--name", "fork")
	fork := fix.startTUI("claude", forkArgs, nil)
	fork.waitFrame("composer", func(s string) bool { return strings.Contains(s, "❯") })
	fork.typeLine("/exit")
	if code := fork.waitExit(); code != 0 {
		t.Fatalf("fork exit %d stderr=%s", code, fork.stderr.String())
	}
	after, err := os.ReadFile(fix.claudeTranscript(fixtureSession))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("fork changed parent")
	}
	data, err := os.ReadFile(fix.claudeTranscript(forkID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"customTitle":"fork"`) ||
		!strings.Contains(string(data), `"sessionId":"`+forkID+`"`) || !strings.Contains(string(data), "one") {
		t.Fatalf("fork transcript %s", data)
	}
}

func TestClaudeForkRecoversParentTitleAndAnnouncesNewIdentity(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.installHooks()
	fix.write(Scenario{BusyMS: intPtr(0)})
	parent := fix.claudeTranscript(fixtureSession)
	if err := os.MkdirAll(filepath.Dir(parent), 0o700); err != nil {
		t.Fatal(err)
	}
	parentTitle := `{"type":"custom-title","customTitle":"P-NAME","sessionId":"` + fixtureSession + `"}` + "\n"
	if err := os.WriteFile(parent, []byte(parentTitle), 0o600); err != nil {
		t.Fatal(err)
	}
	s := fix.startTUI("claude", claudeArgs("--fork-session", "--resume", fixtureSession), map[string]string{
		"TMUX": "/fixture/fork-seat,42,0", "TMUX_PANE": "%0",
	})
	s.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	start := decodeEnvelope(t, fix.waitRecorded(hookSessionStart, 1)[0])
	if start.Source != sourceResume || start.SessionID == fixtureSession ||
		start.TranscriptPath != fix.claudeTranscript(start.SessionID) {
		t.Fatalf("fork SessionStart: %+v", start)
	}
	id, transcript, err := reload.SessionFromCrumb(fix.sidDir, "fork-seat", "%0")
	if err != nil || id != start.SessionID || transcript != start.TranscriptPath {
		t.Fatalf("crumb %q %q %v", id, transcript, err)
	}
	if got := readFile(t, transcript); !strings.Contains(got, `"customTitle":"P-NAME"`) {
		t.Fatalf("fork title: %s", got)
	}
	s.typeLine("/exit")
	if code := s.waitExit(); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

func TestClaudeForkOfAbsentParentStartsEmpty(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{BusyMS: intPtr(0)})
	const forkID = "f3333333-3333-4333-8333-333333333333"
	s := fix.startTUI("claude", claudeArgs("--resume", fixtureSession, "--fork-session", "--session-id", forkID), nil)
	s.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	if got := readFile(t, fix.claudeTranscript(forkID)); got != "" {
		t.Fatalf("fork of absent parent: %s", got)
	}
	s.typeLine("/exit")
	if code := s.waitExit(); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

func TestClaudeResumeRecoversTitle(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.installHooks()
	fix.write(Scenario{BusyMS: intPtr(0)})
	first := fix.startTUI("claude", claudeArgs("--session-id", fixtureSession, "--name", "worker 7"), nil)
	first.waitFrame("composer", func(s string) bool { return strings.Contains(s, "❯") })
	first.typeLine("/exit")
	if code := first.waitExit(); code != 0 {
		t.Fatal(code)
	}
	second := fix.startTUI("claude", claudeArgs("--resume", fixtureSession), nil)
	second.waitFrame("composer", func(s string) bool { return strings.Contains(s, "❯") })
	second.typeLine("go")
	second.waitFrame("reply", func(s string) bool { return strings.Contains(s, DefaultReply) })
	lines := fix.waitRecorded("statusline", 2)
	var input map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &input); err != nil {
		t.Fatal(err)
	}
	if input["session_name"] != "worker 7" {
		t.Fatalf("statusline %v", input["session_name"])
	}
	if n := strings.Count(readFile(t, fix.claudeTranscript(fixtureSession)), `"custom-title"`); n != 1 {
		t.Fatalf("title records %d", n)
	}
}

func TestClaudeResumeTitleStatuslineCases(t *testing.T) {
	for _, test := range []struct {
		name, record, explicit, want string
		noTranscript                 bool
	}{
		{"ai-title", `{"type":"ai-title","aiTitle":"auto title"}`, "", "auto title", false},
		{"explicit-name", `{"type":"custom-title","customTitle":"prior"}`, "OTHER", "OTHER", false},
		{"no-title", `{"type":"user","sessionId":"` + fixtureSession + `"}`, "", "", false},
		{"absent-parent", "", "", "", false},
		{"no-transcript", `{"type":"custom-title","customTitle":"prior"}`, "", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.installHooks()
			fix.write(Scenario{BusyMS: intPtr(0), NoTranscript: test.noTranscript})
			path := fix.claudeTranscript(fixtureSession)
			if test.record != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(test.record+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			args := claudeArgs("--resume", fixtureSession)
			if test.explicit != "" {
				args = claudeArgs("--resume", fixtureSession, "--name", test.explicit)
			}
			s := fix.startTUI("claude", args, nil)
			s.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
			s.typeLine("go")
			s.waitFrame("reply", func(frame string) bool { return strings.Contains(frame, DefaultReply) })
			lines := fix.waitRecorded("statusline", 1)
			var input map[string]any
			if err := json.Unmarshal([]byte(lines[len(lines)-1]), &input); err != nil {
				t.Fatal(err)
			}
			if test.want == "" {
				if _, ok := input["session_name"]; ok {
					t.Fatalf("unexpected session_name: %v", input)
				}
			} else if input["session_name"] != test.want {
				t.Fatalf("session_name=%v want %s", input["session_name"], test.want)
			}
			if test.record != "" && !test.noTranscript {
				got := readFile(t, path)
				if strings.Count(got, `"custom-title"`) > strings.Count(test.record, `"custom-title"`) {
					t.Fatalf("resume wrote title: %s", got)
				}
			}
		})
	}
}

func TestClaudeResumeUnreadableTranscriptExitsUsage(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{BusyMS: intPtr(0)})
	path := fix.claudeTranscript(fixtureSession)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	s := fix.startTUI("claude", claudeArgs("--resume", fixtureSession), nil)
	if code := s.waitExit(); code != ExitUsage || !strings.Contains(s.stderr.String(), path) {
		t.Fatalf("exit %d stderr=%s", code, s.stderr.String())
	}
}
