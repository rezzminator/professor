package mockengine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if _, err := os.Stat(fix.claudeTranscript(forkID)); !os.IsNotExist(err) {
		t.Fatalf("a promptless fork has a transcript (stat: %v); Claude Code writes it at the first prompt", err)
	}
	fork.typeLine("two")
	fork.waitFrame("the fork's reply", func(s string) bool { return strings.Count(s, DefaultReply) == 1 })
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
		!strings.Contains(string(data), `"sessionId":"`+forkID+`"`) || !strings.Contains(string(data), "one") ||
		!strings.Contains(string(data), "two") {
		t.Fatalf("fork transcript %s", data)
	}
}

func TestClaudeForkOfAbsentParentStartsEmpty(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{BusyMS: intPtr(0)})
	const forkID = "f3333333-3333-4333-8333-333333333333"
	s := fix.startTUI("claude", claudeArgs("--resume", fixtureSession, "--fork-session", "--session-id", forkID), nil)
	s.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	if _, err := os.Stat(fix.claudeTranscript(forkID)); !os.IsNotExist(err) {
		t.Fatalf("a promptless fork of an absent parent has a transcript (stat: %v)", err)
	}
	s.typeLine("/exit")
	if code := s.waitExit(); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

// A fixed session id rides a resume only with --fork-session, as Claude Code
// itself refuses it otherwise; with it the fork owns that id.
func TestClaudeSessionIDOnAResumeNeedsAFork(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{BusyMS: intPtr(0)})
	const forkID = "f4444444-4444-4444-8444-444444444444"
	refused := fix.startTUI("claude", claudeArgs("--resume", fixtureSession, "--session-id", forkID), nil)
	if code := refused.waitExit(); code != 1 || refused.stderr.String() != claudeSessionIDRefusal+"\n" {
		t.Fatalf("resume with a session id: exit %d stderr %q, want 1 %q", code, refused.stderr.String(),
			claudeSessionIDRefusal+"\n")
	}
	forkArgs := claudeArgs("--resume", fixtureSession, "--session-id", forkID, "--fork-session")
	forked := fix.startTUI("claude", forkArgs, nil)
	forked.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	forked.typeLine("/exit")
	if code := forked.waitExit(); code != 0 {
		t.Fatalf("fork with a session id: exit %d stderr %q", code, forked.stderr.String())
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
	code := s.waitExit()
	if got, want := strings.NewReplacer(fix.root, "{root}", strings.NewReplacer("/", "-", ".", "-").Replace(fix.work), "{work}").
		Replace(s.stderr.String()),
		"mock-engine: read transcript {root}/cc/projects/{work}/b1111111-1111-4111-8111-111111111111.jsonl: read {root}/cc/projects/{work}/b1111111-1111-4111-8111-111111111111.jsonl: is a directory\n"; code != ExitUsage ||
		got != want {
		t.Fatalf("exit %d stderr = %q, want %q", code, got, want)
	}
}
