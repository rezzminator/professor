package mockengine

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

func TestCodexTurnWritesARolloutPfmIndexesAndRenamesThroughSessionIndex(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureThread, Pane: codexShapes, Steps: []Step{
		{
			Type:   StepTurn,
			Reply:  "incident read",
			BusyMS: 600,
			Tokens: &Tokens{Input: 40, Output: 8, ContextWindow: 258400},
		},
	}})
	session := fix.startTUI("codex", codexArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "›") })
	session.typeLine("read the incident report")
	session.waitFrame(
		"the supplied busy shape",
		func(frame string) bool { return strings.Contains(frame, codexShapes.Busy) },
	)
	session.waitFrame("the reply", func(frame string) bool {
		return strings.Contains(frame, "incident read") && !strings.Contains(frame, codexShapes.Busy)
	})

	rollout := fix.rolloutPath()
	header, err := codexmeta.ReadHeader(rollout)
	if err != nil {
		t.Fatalf("codexmeta.ReadHeader: %v", err)
	}
	if header.Kind != codexmeta.User || header.ID != fixtureThread {
		t.Fatalf("header = %+v, want a user thread %s", header, fixtureThread)
	}
	rows, names := fix.indexedRollouts()
	if len(rows) != 1 || rows[0].ID != fixtureThread || !rows[0].UserThread || rows[0].PromptCount != 1 ||
		rows[0].FirstPrompt != "read the incident report" || rows[0].CWD != fix.work {
		t.Fatalf("indexed rollouts = %+v", rows)
	}
	if len(names) != 0 {
		t.Fatalf("a thread nobody renamed has a name: %v", names)
	}
	meta, err := transcript.ReadMeta(rollout, string(pfmengine.Codex))
	if err != nil {
		t.Fatal(err)
	}
	if meta.ContextTokens != 48 || meta.ContextWindow != 258400 {
		t.Fatalf("ReadMeta codex = %+v, want token_count 48 in a 258400 window", meta)
	}
	last := transcript.Entry{}
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, rollout)), "\n") {
		if entry, ok := transcript.Parse([]byte(line), string(pfmengine.Codex)); ok {
			last = entry
		}
	}
	if last.Role != transcript.RoleAssistant || last.Text != "incident read" {
		t.Fatalf("last rollout entry = %+v, want the assistant reply", last)
	}

	session.typeLine("/rename _KILL codex worker")
	indexPath := filepath.Join(fix.codexHome, codexmeta.SessionIndexFile)
	row := waitFile(t, indexPath, func(content string) bool { return strings.Contains(content, "_KILL") })
	entry, err := codexmeta.DecodeSessionIndexLine([]byte(strings.TrimSpace(row)))
	if err != nil {
		t.Fatalf("pfm's session_index decoder refused the row %q: %v", row, err)
	}
	if _, ok := entry.RenamedAt(); entry.ID != fixtureThread || entry.ThreadName != "_KILL codex worker" || !ok {
		t.Fatalf("session_index entry = %+v, want id, name and a rename time", entry)
	}
	if _, names = fix.indexedRollouts(); names[fixtureThread] != "_KILL codex worker" {
		t.Fatalf("indexed Codex names = %v", names)
	}
}

// stdioProfessorArg re-executes this test binary as the stdio server a Codex
// config.toml names: a chat-only professor server over RunStdio, no daemon.
const stdioProfessorArg = "mockengine-stdio-professor"

func TestCodexRolloutHandleLivesForTheSession(t *testing.T) {
	fix := newFixture(t)
	proc := &process{
		ctx: context.Background(), engine: engineCodex, env: fix.env(nil), cwd: fix.work,
		pid: os.Getpid(), stderr: io.Discard, script: newScript(Scenario{}, engineCodex),
	}
	var first string
	for _, testCase := range []struct {
		name              string
		resumed, compress bool
	}{{"fresh", false, false}, {"resumed", true, false}, {"resumed from a compressed rollout", true, true}} {
		resumed := testCase.resumed
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.compress {
				if _, err := codexmeta.CompressRollout(first); err != nil {
					t.Fatal(err)
				}
			}
			session := &codexSession{proc: proc, codexHome: fix.codexHome, threadID: fixtureThread, model: "fixture"}
			if err := session.start(resumed); err != nil {
				t.Fatal(err)
			}
			if first == "" {
				first = session.rollout
			}
			if session.rollout != first {
				t.Fatalf("rollout = %s, want the thread's first rollout %s", session.rollout, first)
			}
			if testCase.compress {
				if content := readFile(t, first); strings.Count(content, `"type":"session_meta"`) != 1 {
					t.Fatalf("resume of a compressed rollout did not decompress it in place: %s", content)
				}
				if _, err := os.Stat(first + ".zst"); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("compressed rollout %s.zst after resume: %v, want it removed", first, err)
				}
			}
			t.Cleanup(func() {
				if err := session.finish("other"); err != nil {
					t.Errorf("finish: %v", err)
				}
			})
			if count := rolloutHandles(t, session.rollout); count != 1 {
				t.Fatalf("start holds %d rollout handles, want one", count)
			}
			moved := session.rollout + ".held"
			if err := os.Rename(session.rollout, moved); err != nil {
				t.Fatal(err)
			}
			if err := session.recordUser("held prompt"); err != nil {
				t.Fatal(err)
			}
			if err := session.recordAssistant("held reply", Tokens{Input: 2, Output: 3}); err != nil {
				t.Fatal(err)
			}
			if content := readFile(t, moved); !strings.Contains(content, "held prompt") ||
				!strings.Contains(content, "held reply") || !strings.Contains(content, "token_count") {
				t.Fatalf("records did not use the held handle: %s", content)
			}
			if err := os.Rename(moved, session.rollout); err != nil {
				t.Fatal(err)
			}
			if err := session.finish("other"); err != nil {
				t.Fatal(err)
			}
			if count := rolloutHandles(t, session.rollout); count != 0 {
				t.Fatalf("finish left %d rollout handles", count)
			}
		})
	}
}

func TestCodexRolloutClosesOnEveryPaneExit(t *testing.T) {
	for _, exit := range []string{"command", "eof", "cancel", "crash", "step", "start failure"} {
		t.Run(exit, func(t *testing.T) {
			fix := newFixture(t)
			scenario := Scenario{SessionID: fixtureThread, Pane: codexShapes}
			if exit == "crash" || exit == "step" {
				kind := StepCrash
				if exit == "step" {
					kind = StepExit
				}
				scenario.Steps = []Step{{Type: kind, ExitCode: 7}}
			}
			if exit == "start failure" {
				blocked := filepath.Join(fix.root, "blocked")
				if err := os.WriteFile(blocked, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				scenario.Jail = Jail{ProcRoot: blocked, SIDDir: fix.sidDir}
			}
			fix.write(scenario)
			session := fix.startTUI("codex", codexArgs(), nil)
			wantCode := 0
			if exit != "start failure" {
				session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "›") })
				path := fix.rolloutPath()
				if count := rolloutHandles(t, path); count != 1 {
					t.Fatalf("pane holds %d rollout handles, want one", count)
				}
			}
			switch exit {
			case "command":
				session.typeLine("/exit")
			case "eof":
				if err := session.stdin.Close(); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				session.cancel()
			case "crash", "step":
				session.typeLine("leave")
				wantCode = 7
			case "start failure":
				wantCode = ExitUsage
			}
			if code := session.waitExit(); code != wantCode {
				t.Fatalf("exit code = %d, want %d", code, wantCode)
			}
			if count := rolloutHandles(t, fix.rolloutPath()); count != 0 {
				t.Fatalf("%s left %d rollout handles", exit, count)
			}
		})
	}
}
