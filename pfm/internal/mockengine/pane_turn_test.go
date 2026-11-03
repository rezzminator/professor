package mockengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/rowfacts"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

func TestUnknownReloadKeepsEngineAlive(t *testing.T) {
	for _, engine := range []string{engineCodex, engineOpenCode} {
		t.Run(engine, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			shape, glyph := codexShapes, "›"
			if engine == engineOpenCode {
				shape, glyph = openCodeShapes, openCodeShapes.Composer
			}
			fix.write(
				Scenario{
					Pane:   shape,
					BusyMS: intPtr(0),
					Steps:  []Step{{Type: StepTurn, Reply: "native prompt response"}},
				},
			)
			session := fix.startTUI(engine, nil, nil)
			session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, glyph) })
			session.typeLine("/reload")
			if engine == engineCodex {
				want := `Unrecognized command '/reload'. Type "/" for a list of supported commands.`
				session.waitFrame("local rejection and restored draft", func(frame string) bool {
					return strings.Contains(frame, want) && strings.Contains(frame, "› /reload")
				})
				meta, err := transcript.ReadMeta(fix.rolloutPath(), string(pfmengine.Codex))
				if err != nil || meta.HumanPrompts != 0 {
					t.Fatalf("unknown slash meta=%+v err=%v, want 0 human prompts", meta, err)
				}
				session.typeRaw(strings.Repeat("\x7f", len("/reload")))
				session.typeLine("ordinary")
			}
			session.waitFrame(
				"scripted response",
				func(frame string) bool { return strings.Contains(frame, "native prompt response") },
			)
			if got := readFile(t, fix.scenario+cursorSuffix); got != "1" {
				t.Fatalf("%s cursor=%q, want 1", fix.scenario, got)
			}
			session.typeLine("/exit")
			if code := session.waitExit(); code != 0 {
				t.Fatalf("%s exit=%d stderr=%q, want 0", engine, code, session.stderr.String())
			}
		})
	}
}

func TestClaudeTurnShowsABusyFooterThenAReplyAndWritesTheTranscript(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{
		SessionID: fixtureSession,
		Steps: []Step{
			{
				Type:   StepTurn,
				Reply:  "granite",
				BusyMS: 700,
				Tokens: &Tokens{Input: 20, Output: 5, CacheRead: 300, CacheCreation: 7},
			},
		},
	})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	if pfmcall.IsBusy(session.out.frame()) {
		t.Fatalf("an idle composer reads busy:\n%s", session.out.frame())
	}
	session.typeLine("hello there")
	session.waitFrame("the busy footer pfmcall.IsBusy matches", pfmcall.IsBusy)
	frame := session.waitFrame("the reply with the footer gone", func(frame string) bool {
		return !pfmcall.IsBusy(frame) && strings.Contains(frame, "granite")
	})
	if pfmcall.SelectorLine(frame) != "" {
		t.Fatalf("an idle reply frame reads as an open menu: %q", pfmcall.SelectorLine(frame))
	}

	path := fix.claudeTranscript(fixtureSession)
	rowsWithFacts, failures := rowfacts.NewReader(fix.sidDir).
		Enrich([]compose.Row{{Kind: compose.LiveClaude, ID: fixtureSession, Path: path}}, time.Now().UnixNano())
	if len(failures) != 0 || rowsWithFacts[0].Working || rowsWithFacts[0].Model != "claude-sonnet-4-5-fixture" {
		t.Fatalf(
			"picker facts=%+v errors=%v, want completed Claude row idle with fixture model",
			rowsWithFacts,
			failures,
		)
	}
	meta := readMeta(t, path)
	if meta.HumanPrompts != 1 || meta.Model != "claude-sonnet-4-5-fixture" || meta.ContextTokens != 20+300+7 {
		t.Fatalf("ReadMeta = %+v, want 1 human prompt, the --model word, 327 context tokens", meta)
	}
	rows := fix.indexedTranscripts()
	if len(rows) != 1 || rows[0].UUID != fixtureSession || rows[0].CWD != fix.work ||
		rows[0].FirstPrompt != "hello there" || rows[0].PromptCount != 1 || rows[0].IsBG {
		t.Fatalf("indexed transcripts = %+v, want one interactive row for %s in %s", rows, fixtureSession, fix.work)
	}
	last := ""
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, path)), "\n") {
		if entry, ok := transcript.Parse([]byte(line), string(pfmengine.Claude)); ok {
			last = entry.Role + ":" + entry.Text
		}
	}
	if last != transcript.RoleAssistant+":granite" {
		t.Fatalf("last transcript entry = %q, want the assistant reply", last)
	}
}

func TestClaudeHoldStaysBusyUntilTheGateFileIsGone(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	gate := filepath.Join(fix.root, "gate")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepHold, UntilGone: gate},
		{Type: StepTurn, Reply: "released"},
	}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("wait for me")
	session.waitFrame("busy while the gate exists", pfmcall.IsBusy)
	time.Sleep(400 * time.Millisecond)
	if frame := session.out.frame(); !pfmcall.IsBusy(frame) || strings.Contains(frame, "released") {
		t.Fatalf("the hold released before the gate went:\n%s", frame)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	session.waitFrame("the reply once the gate is gone", func(frame string) bool {
		return !pfmcall.IsBusy(frame) && strings.Contains(frame, "released")
	})
}

// TestClaudeHoldAbortsOnANonAbsenceStatError covers F3: a stat failure that
// is not the gate's confirmed absence (here ENOTDIR, a component of the path
// is a regular file) must abort the hold with a named failure, never release
// it the way a genuine removal does.

func TestClaudeHoldAbortsOnANonAbsenceStatError(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	notADir := filepath.Join(fix.root, "not-a-dir")
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(notADir, "gate")
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepHold, UntilGone: gate},
		{Type: StepTurn, Reply: "unreachable"},
	}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("wait for me")
	if code := session.waitExit(); code != ExitUnpinned {
		t.Fatalf("exit = %d, want %d (a non-absence stat error aborts the hold)", code, ExitUnpinned)
	}
	if !strings.Contains(session.stderr.String(), "hold") {
		t.Fatalf("stderr = %q, want it to name the failed hold", session.stderr.String())
	}
}
