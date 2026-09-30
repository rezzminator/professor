package mockengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/chat"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless/run"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/inject"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

const fixtureSession = "b1111111-1111-4111-8111-111111111111"

const verbsStdioProfessorArg = "mockengine-verbs-stdio-professor"

func init() {
	if len(os.Args) < 2 || os.Args[1] != verbsStdioProfessorArg {
		return
	}
	os.Exit(serveVerbsStdioProfessor())
}

func serveVerbsStdioProfessor() int {
	resolved, err := paths.Resolve()
	if err != nil {
		fmt.Fprintf(os.Stderr, "verbs stdio professor: resolve paths: %v\n", err)
		return 1
	}
	service, err := mcpserv.NewConfigured("test", os.Stderr, mcpserv.Runtime{
		Paths: resolved, Chat: chat.Verbs{Warnings: os.Stderr}, AllowAmbientIdentity: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "verbs stdio professor: configure chat: %v\n", err)
		return 1
	}
	defer func() {
		if err := service.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "verbs stdio professor: close chat: %v\n", err)
		}
	}()
	professor, err := mcpserv.NewProfessor(mcpserv.ProfessorOptions{Version: "test", Chat: service})
	if err != nil {
		fmt.Fprintf(os.Stderr, "verbs stdio professor: %v\n", err)
		return 1
	}
	if err := professor.RunStdio(context.Background(), os.Stdin, os.Stdout, mcpserv.StdioOptions{
		Home: resolved.Home, SIDDir: resolved.SIDDir, Warnings: os.Stderr,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "verbs stdio professor: %v\n", err)
		return 1
	}
	return 0
}

// claudeArgs is the argv pfm's spawn template hands a Claude chat
// (internal/action/claude_spawn.go): the caller's words, then LaunchArgs.
func claudeArgs(extra ...string) []string {
	return append(
		extra,
		"--settings",
		`{"env":{"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"100000","CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"1"},"outputStyle":"default"}`,
		"--model",
		"claude-sonnet-4-5-fixture",
	)
}

func (fix *fixture) claudeTranscript(sessionID string) string {
	return filepath.Join(fix.configDir, "projects", claudeProjectSlug(fix.work), sessionID+".jsonl")
}

// indexedTranscripts runs pfm's own Claude indexer over the jail and returns
// what it stored — the judge of every transcript the mock wrote.
func (fix *fixture) indexedTranscripts() []store.Transcript {
	fix.t.Helper()
	database, err := store.Open(store.WithWarningWriter(os.Stderr))
	if err != nil {
		fix.t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			fix.t.Errorf("close store: %v", err)
		}
	}()
	ctx := context.Background()
	counters := index.Counters{}
	if err := index.SyncClaude(
		ctx,
		database,
		[]string{filepath.Join(fix.configDir, "projects")},
		&counters,
	); err != nil {
		fix.t.Fatalf("index Claude transcripts: %v", err)
	}
	if counters.FilesSeen == 0 {
		fix.t.Fatalf("the indexer saw no transcript under %s", filepath.Join(fix.configDir, "projects"))
	}
	rows, err := database.Transcripts(ctx)
	if err != nil {
		fix.t.Fatal(err)
	}
	return rows
}

func readMeta(t *testing.T, path string) transcript.Meta {
	t.Helper()
	meta, err := transcript.ReadMeta(path, string(pfmengine.Claude))
	if err != nil {
		t.Fatalf("ReadMeta %s: %v", path, err)
	}
	return meta
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
	if inject.IsBusy(session.out.frame()) {
		t.Fatalf("an idle composer reads busy:\n%s", session.out.frame())
	}
	session.typeLine("hello there")
	session.waitFrame("the busy footer inject.IsBusy matches", inject.IsBusy)
	frame := session.waitFrame("the reply with the footer gone", func(frame string) bool {
		return !inject.IsBusy(frame) && strings.Contains(frame, "granite")
	})
	if inject.SelectorLine(frame) != "" {
		t.Fatalf("an idle reply frame reads as an open menu: %q", inject.SelectorLine(frame))
	}

	path := fix.claudeTranscript(fixtureSession)
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

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestClaudeNameFlagAndRenameLandAsCustomTitle(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0)})
	session := fix.startTUI("claude", claudeArgs("--name", "worker 7", "audit the firewall"), nil)
	session.waitFrame("the launch prompt answered", func(frame string) bool {
		return strings.Contains(frame, DefaultReply) && !inject.IsBusy(frame)
	})
	rows := fix.indexedTranscripts()
	if len(rows) != 1 || rows[0].CustomTitle != "worker 7" || rows[0].FirstPrompt != "audit the firewall" {
		t.Fatalf("indexed = %+v, want title from --name and the launch prompt recorded", rows)
	}
	session.typeLine("/rename fresh-name")
	waitFile(t, fix.claudeTranscript(fixtureSession), func(content string) bool {
		return strings.Contains(content, "fresh-name")
	})
	rows = fix.indexedTranscripts()
	if len(rows) != 1 || rows[0].CustomTitle != "fresh-name" || rows[0].PromptCount != 1 {
		t.Fatalf("indexed after /rename = %+v, want the new title and no extra prompt", rows)
	}
}

func intPtr(value int) *int { return &value }

func TestClaudeMenuIsAnOpenSelectorUntilAnswered(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession, Steps: []Step{
		{Type: StepMenu, Options: []string{"Yes", "No, and tell Claude what to do differently"}, Selected: 1},
	}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("do the thing")
	frame := session.waitFrame("an open menu inject.SelectorLine reads", func(frame string) bool {
		return inject.SelectorLine(frame) != ""
	})
	if selector := inject.SelectorLine(frame); !strings.Contains(selector, "1. Yes") {
		t.Fatalf("selector line = %q, want the preselected first option", selector)
	}
	if inject.IsBusy(frame) {
		t.Fatalf("an open menu must not read as a busy turn:\n%s", frame)
	}
	session.typeRaw("\r")
	session.waitFrame("the menu closed", func(frame string) bool {
		return inject.SelectorLine(frame) == "" && strings.Contains(frame, "❯")
	})
}

func TestClaudeCompactPrintsTheReceiptAndMarksTheTranscript(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepTurn, Reply: "first"},
		{Type: StepCompact, PostTokens: 777},
	}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("warm up")
	session.waitFrame("the first reply", func(frame string) bool { return strings.Contains(frame, "first") })
	if inject.CompactionReceipt(session.out.frame()) {
		t.Fatal("a receipt is showing before any compaction")
	}
	session.typeLine("/compact")
	session.waitFrame("the compaction receipt inject.CompactionReceipt matches", inject.CompactionReceipt)
	meta := readMeta(t, fix.claudeTranscript(fixtureSession))
	if !meta.CompactedAfterUsage || meta.PostCompactTokens != 777 || meta.HumanPrompts != 1 {
		t.Fatalf("ReadMeta = %+v, want a compaction after usage with 777 post tokens and 1 human prompt", meta)
	}
}

// exitDialogPattern is internal/reload/reload.go:535's spelling of the
// background-work exit confirmation, repeated here because reload keeps it
// unexported.
var exitDialogPattern = regexp.MustCompile(`❯[\s\v]*\d+\.[\s\v]*Exit`)

func TestClaudeBackgroundAgentIsIdleAndExitAsksToStopIt(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepBackgroundAgent, Name: "tracer", Status: "mapping the seams"},
		{Type: StepTurn, Reply: "dispatched"},
	}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("map it")
	frame := session.waitFrame("the reply beside an agent row", func(frame string) bool {
		return strings.Contains(frame, "dispatched") && strings.Contains(frame, "● tracer")
	})
	if inject.IsBusy(frame) {
		t.Fatalf("a background agent must leave the main turn idle:\n%s", frame)
	}
	if inject.SelectorLine(frame) != "" {
		t.Fatalf("the agent row read as a menu: %q", inject.SelectorLine(frame))
	}
	session.typeLine("/exit")
	frame = session.waitFrame("the exit confirmation reload.go:535 presses Enter on", exitDialogPattern.MatchString)
	if !strings.Contains(inject.SelectorLine(frame), "Exit") {
		t.Fatalf("selector = %q, want the Exit row preselected", inject.SelectorLine(frame))
	}
	session.typeRaw("\r")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit code after confirming = %d", code)
	}
	meta := readMeta(t, fix.claudeTranscript(fixtureSession))
	rows := fix.indexedTranscripts()
	if meta.HumanPrompts != 1 || len(rows) != 1 || rows[0].PromptCount != 1 {
		t.Fatalf("sidechain records leaked into the count: meta=%+v rows=%+v", meta, rows)
	}
}

func TestClaudeExitWithoutBackgroundWorkQuitsAtOnce(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if exitDialogPattern.MatchString(session.out.all()) {
		t.Fatal("an exit dialog appeared with no background work to stop")
	}
}

func TestClaudeCrashStepExitsMidTurnWithItsCode(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession, Steps: []Step{{Type: StepCrash, ExitCode: 7}}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("go")
	if code := session.waitExit(); code != 7 {
		t.Fatalf("exit code = %d, want the crash step's 7", code)
	}
	if meta := readMeta(t, fix.claudeTranscript(fixtureSession)); meta.HumanPrompts != 1 {
		t.Fatalf("the prompt that crashed the turn was not recorded: %+v", meta)
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
	session.waitFrame("busy while the gate exists", inject.IsBusy)
	time.Sleep(400 * time.Millisecond)
	if frame := session.out.frame(); !inject.IsBusy(frame) || strings.Contains(frame, "released") {
		t.Fatalf("the hold released before the gate went:\n%s", frame)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	session.waitFrame("the reply once the gate is gone", func(frame string) bool {
		return !inject.IsBusy(frame) && strings.Contains(frame, "released")
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

func TestClaudeResumeAppendsToTheSameTranscript(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{BusyMS: intPtr(0)})
	first := fix.startTUI("claude", claudeArgs("--session-id", fixtureSession), nil)
	first.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	first.typeLine("one")
	first.waitFrame("the first reply", func(frame string) bool { return strings.Contains(frame, DefaultReply) })
	first.typeLine("/exit")
	if code := first.waitExit(); code != 0 {
		t.Fatalf("first exit = %d", code)
	}
	second := fix.startTUI("claude", claudeArgs("--resume", fixtureSession), nil)
	second.waitFrame("the resumed composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	second.typeLine("two")
	second.waitFrame("the second reply", func(frame string) bool { return strings.Contains(frame, DefaultReply) })
	if meta := readMeta(t, fix.claudeTranscript(fixtureSession)); meta.HumanPrompts != 2 {
		t.Fatalf("resumed transcript ReadMeta = %+v, want 2 human prompts in one file", meta)
	}
	if rows := fix.indexedTranscripts(); len(rows) != 1 || rows[0].PromptCount != 2 || rows[0].LastPrompt != "two" {
		t.Fatalf("indexed = %+v, want one row with both prompts", rows)
	}
}

func TestClaudeSessionIDFlagNamesTheTranscript(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0)})
	const selected = "c2222222-2222-4222-8222-222222222222"
	session := fix.startTUI("claude", claudeArgs("--session-id", selected), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("chosen id")
	session.waitFrame("the reply", func(frame string) bool { return strings.Contains(frame, DefaultReply) })
	if meta := readMeta(t, fix.claudeTranscript(selected)); meta.HumanPrompts != 1 {
		t.Fatalf("selected transcript meta=%+v", meta)
	}
	if rows := fix.indexedTranscripts(); len(rows) != 1 || rows[0].UUID != selected {
		t.Fatalf("indexed transcripts=%+v, want selected id", rows)
	}
}

func TestClaudeHeadlessEnvelopeIsReadByPfmsOwnRunner(t *testing.T) {
	fix := newFixture(t)
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepTurn, Reply: "forty-two", Tokens: &Tokens{Input: 11, Output: 4, CacheRead: 100, CacheCreation: 9}},
		{Type: StepTurn, Structured: json.RawMessage(`{"ok":true}`)},
	}})
	machine := pfmconfig.Config{
		Claude:   pfmconfig.ClaudePrefs{Binary: "claude"},
		Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: fix.configDir}},
	}
	result, err := run.Run(context.Background(), run.Request{
		Config:  machine,
		Engine:  pfmengine.Claude,
		Prompt:  "what is the answer",
		CWD:     fix.work,
		Timeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("headless run: %v (stdout=%q stderr=%q)", err, result.Stdout, result.Stderr)
	}
	if result.Answer != "forty-two" || result.ExitCode != 0 || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if result.Usage == nil || result.Usage.Input != 11 || result.Usage.Output != 4 ||
		result.Usage.CachedInput != 100 || result.Usage.CacheCreation != 9 {
		t.Fatalf("usage = %+v, want the step's tokens through modelUsage", result.Usage)
	}
	if result.TotalCostUSD == nil || *result.TotalCostUSD <= 0 {
		t.Fatalf("total cost = %v, want a positive receipt", result.TotalCostUSD)
	}
	if meta := readMeta(t, fix.claudeTranscript(fixtureSession)); meta.HumanPrompts != 1 {
		t.Fatalf("a headless turn left no transcript: %+v", meta)
	}

	structured, err := run.Run(context.Background(), run.Request{
		Config: machine, Engine: pfmengine.Claude, Prompt: "shape it", CWD: fix.work, Timeout: 20 * time.Second,
		Schema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}`),
	})
	if err != nil {
		t.Fatalf("structured headless run: %v (stdout=%q)", err, structured.Stdout)
	}
	if string(structured.StructuredOutput) != `{"ok":true}` {
		t.Fatalf("structured_output = %s", structured.StructuredOutput)
	}
}

func TestClaudeHeadlessNoSessionPersistenceWritesNoTranscript(t *testing.T) {
	fix := newFixture(t)
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0)})
	machine := pfmconfig.Config{
		Claude:   pfmconfig.ClaudePrefs{Binary: "claude"},
		Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: fix.configDir}},
	}
	result, err := run.Run(context.Background(), run.Request{
		Config: machine, Engine: pfmengine.Claude, Prompt: "ephemeral", CWD: fix.work,
		Timeout: 20 * time.Second, NoSessionPersistence: true,
	})
	if err != nil || result.Answer != DefaultReply {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(fix.claudeTranscript(fixtureSession)); !os.IsNotExist(err) {
		t.Fatalf("a --no-session-persistence run wrote a transcript (stat err=%v)", err)
	}
}

func TestClaudeInlineTurnsKeepThePositionalCursor(t *testing.T) {
	for _, row := range []struct {
		name, directive, reply string
		refused                bool
	}{
		{"object", `{"type":"turn","reply":"R1"}`, "R1", false},
		{"compact", `{"type":"compact"}`, "fallback", false},
		{"unknown type", `{"type":"nope"}`, "unknown step type", true},
		{"broken JSON", `{"type":`, "", true},
		{"unknown field", `{"type":"turn","delay":3}`, "unknown field", true},
		{"trailing JSON", `{"type":"turn"} {}`, "", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.write(Scenario{SessionID: fixtureSession, Reply: "fallback", BusyMS: intPtr(0), Steps: []Step{
				{Type: StepTurn, Reply: "positional"},
			}})
			session := fix.startTUI("claude", claudeArgs(), nil)
			session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
			prompt := "hi mock-engine: " + row.directive
			session.typeLine(prompt)
			frame := session.waitFrame("a completed turn", func(frame string) bool {
				return !inject.IsBusy(frame) && strings.Contains(frame, "⏺ ")
			})
			want := "⏺ " + row.reply
			if row.refused {
				want = "⏺ mock-engine: inline steps refused — "
			}
			if !strings.Contains(frame, want) || !strings.Contains(frame, row.reply) {
				t.Fatalf("inline reply lacks %q and %q:\n%s", want, row.reply, frame)
			}
			entries := parsedEntries(t, fix.claudeTranscript(fixtureSession))
			if len(entries) != 2 || entries[0].Text != prompt {
				t.Fatalf("inline transcript = %+v, want the whole prompt and a reply", entries)
			}
			if _, err := os.Stat(fix.scenario + cursorSuffix); !os.IsNotExist(err) {
				t.Fatalf("inline turn moved the positional cursor: %v", err)
			}
			if row.name == "compact" &&
				strings.Count(readFile(t, fix.claudeTranscript(fixtureSession)), `"compact_boundary"`) != 1 {
				t.Fatal("the inline compaction did not run")
			}
			session.typeLine("plain prompt")
			session.waitFrame("the pending positional reply", func(frame string) bool {
				return strings.Contains(frame, "⏺ positional")
			})
			if got := readFile(t, fix.scenario+cursorSuffix); got != "1" {
				t.Fatalf("plain turn cursor=%q, want 1", got)
			}
		})
	}
}

func TestClaudeInlineArrayAndQueuedPromptsHaveTheirOwnSteps(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(
		Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{{Type: StepTurn, Reply: "positional"}}},
	)
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	first := `first mock-engine: [{"type":"background_agent","name":"a"},{"type":"turn","reply":"R2","busy_ms":500}]`
	second := `queued mock-engine: {"type":"turn","reply":"queued-reply"}`
	session.typeLine(first)
	session.waitFrame("the busy turn", inject.IsBusy)
	session.typeLine(second)
	session.typeLine("plain queued prompt")
	session.waitFrame("all queued turns", func(frame string) bool { return strings.Contains(frame, "⏺ positional") })
	frame := session.out.frame()
	for _, needle := range []string{"⏺ R2", "⏺ queued-reply", "❯ ● a  running (background)"} {
		if !strings.Contains(frame, needle) {
			t.Fatalf("queued pane lacks %q:\n%s", needle, frame)
		}
	}
	entries := parsedEntries(t, fix.claudeTranscript(fixtureSession))
	if len(entries) != 6 || entries[0].Text != first || entries[2].Text != second || entries[5].Text != "positional" {
		t.Fatalf("queued transcript = %+v", entries)
	}
	sidechains, err := filepath.Glob(
		filepath.Join(strings.TrimSuffix(fix.claudeTranscript(fixtureSession), ".jsonl"), "subagents", "*.jsonl"),
	)
	if err != nil || len(sidechains) != 1 || !strings.Contains(readFile(t, sidechains[0]), `"isSidechain":true`) {
		t.Fatalf("sidechains=%v err=%v", sidechains, err)
	}
	if got := readFile(t, fix.scenario+cursorSuffix); got != "1" {
		t.Fatalf("queued cursor=%q, want only the plain turn consumed", got)
	}
}

func TestClaudeInlineHeadlessEnvelopeIsReadByPfmsOwnRunner(t *testing.T) {
	for _, directive := range []string{`{"type":"turn","reply":"H1","structured":{"word":"w"}}`, `{"type":"nope"}`, `{"type":`} {
		t.Run(directive, func(t *testing.T) {
			fix := newFixture(t)
			fix.write(
				Scenario{
					SessionID: fixtureSession,
					BusyMS:    intPtr(0),
					Steps:     []Step{{Type: StepTurn, Reply: "positional"}},
				},
			)
			machine := pfmconfig.Config{
				Claude:   pfmconfig.ClaudePrefs{Binary: "claude"},
				Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: fix.configDir}},
			}
			prompt := "headless mock-engine: " + directive
			var schema json.RawMessage
			if strings.Contains(directive, "H1") {
				schema = json.RawMessage(`{"type":"object","properties":{"word":{"type":"string"}}}`)
			}
			result, err := run.Run(context.Background(), run.Request{
				Config: machine, Engine: pfmengine.Claude, Prompt: prompt, CWD: fix.work, Timeout: 20 * time.Second,
				Schema: schema,
			})
			if err != nil || result.ExitCode != 0 || result.IsError {
				t.Fatalf("inline headless=%+v err=%v", result, err)
			}
			if strings.Contains(directive, "H1") {
				if result.Answer != "H1" || string(result.StructuredOutput) != `{"word":"w"}` {
					t.Fatalf("inline envelope=%+v", result)
				}
			} else if !strings.HasPrefix(result.Answer, "mock-engine: inline steps refused — ") {
				t.Fatalf("refusal=%q", result.Answer)
			}
			if _, err := os.Stat(fix.scenario + cursorSuffix); !os.IsNotExist(err) {
				t.Fatalf("headless inline consumed the positional cursor: %v", err)
			}
			entries := parsedEntries(t, fix.claudeTranscript(fixtureSession))
			if len(entries) != 2 || entries[0].Text != prompt {
				t.Fatalf("headless records=%+v", entries)
			}
		})
	}
}

func TestClaudeMCPStepCallsPfmsOwnServer(t *testing.T) {
	for _, fileConfig := range []bool{false, true} {
		t.Run(fmt.Sprint(fileConfig), func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			identityFile := filepath.Join(fix.recordDir, "mcp-identity")
			config := string(mustJSON(t, map[string]any{"mcpServers": map[string]any{"professor": map[string]any{
				"type": "stdio", "command": "sh", "args": []string{
					"-c",
					fmt.Sprintf(
						`printf '%%s\n' "$TMUX" "$TMUX_PANE" "$CLAUDE_CODE_SESSION_ID" "$PWD" > %q; exec "$@"`,
						identityFile,
					),
					"fixture",
					executable,
					verbsStdioProfessorArg,
				},
			}}}))
			if fileConfig {
				path := filepath.Join(fix.root, "mcp.json")
				if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
					t.Fatal(err)
				}
				config = path
			}
			fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0)})
			session := fix.startTUI(
				"claude",
				claudeArgs("--mcp-config", config),
				map[string]string{"TMUX": "/fixture/cc-fixture,42,0", "TMUX_PANE": "%7"},
			)
			session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
			session.typeLine("violet archive fixture")
			session.waitFrame("the seed reply", func(frame string) bool { return strings.Contains(frame, "⏺ ok") })
			fix.indexedTranscripts()
			session.typeLine(
				`query mock-engine: [{"type":"mcp","tool":"chat_find","input":{"excerpt":"violet archive fixture","include_self":true}},{"type":"turn","reply":"MCP finished"}]`,
			)
			session.waitFrame(
				"the MCP reply",
				func(frame string) bool { return strings.Contains(frame, "⏺ MCP finished") },
			)
			result := readFile(t, filepath.Join(fix.recordDir, "mcp-call-chat_find.json"))
			if !strings.Contains(result, fixtureSession) {
				t.Fatalf("pfm's tools/call answer=%s", result)
			}
			var uses, results int
			for _, line := range strings.Split(strings.TrimSpace(readFile(t, fix.claudeTranscript(fixtureSession))), "\n") {
				var record struct {
					Message struct{ Content json.RawMessage }
				}
				if err := json.Unmarshal([]byte(line), &record); err != nil {
					t.Fatal(err)
				}
				if len(record.Message.Content) == 0 || record.Message.Content[0] != '[' {
					continue
				}
				var parts []contentPart
				if err := json.Unmarshal(record.Message.Content, &parts); err != nil {
					t.Fatal(err)
				}
				for _, part := range parts {
					if part.Type == "tool_use" && part.Name == "chat_find" {
						uses++
					}
					if part.Type == "tool_result" && strings.Contains(part.Content, fixtureSession) && !part.IsError {
						results++
					}
				}
			}
			if uses != 1 || results != 1 {
				t.Fatalf("tool records: use=%d result=%d", uses, results)
			}
			if got := readFile(
				t,
				identityFile,
			); got != "/fixture/cc-fixture,42,0\n%7\n"+fixtureSession+"\n"+fix.work+"\n" {
				t.Fatalf("MCP child identity=%q", got)
			}
		})
	}
}

func TestClaudeMCPFailuresAreToolResultsAndKeepThePaneLive(t *testing.T) {
	for _, row := range []struct{ name, config, tool, cause string }{
		{"missing server", `{"mcpServers":{}}`, "chat_find", "professor"},
		{"broken config", `{`, "chat_find", "decode"},
		{"spawn", `{"mcpServers":{"professor":{"command":"/tmp/mockengine-command-absent"}}}`, "chat_find", "initialize"},
		{"call", "", "unknown_fixture_tool", "tools/call"},
	} {
		t.Run(row.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			config := row.config
			if config == "" {
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				config = string(
					mustJSON(
						t,
						map[string]any{
							"mcpServers": map[string]any{
								"professor": map[string]any{
									"command": executable,
									"args":    []string{verbsStdioProfessorArg},
								},
							},
						},
					),
				)
			}
			fix.write(
				Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{{Type: StepMCP, Tool: row.tool}}},
			)
			session := fix.startTUI("claude", claudeArgs("--mcp-config", config), nil)
			session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
			session.typeLine("call the tool")
			session.waitFrame(
				"the turn after the failed tool",
				func(frame string) bool { return strings.Contains(frame, "⏺ ok") },
			)
			content := readFile(t, fix.claudeTranscript(fixtureSession))
			if !strings.Contains(content, `"is_error":true`) || !strings.Contains(content, row.cause) {
				t.Fatalf("failure result lacks %q: %s", row.cause, content)
			}
			session.typeLine("still alive")
			waitFile(
				t,
				fix.claudeTranscript(fixtureSession),
				func(content string) bool { return strings.Contains(content, "still alive") },
			)
		})
	}
}

func TestClaudeMCPWithoutAToolKeepsItsNamedRefusal(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{Steps: []Step{{Type: StepMCP}}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("handshake")
	if code := session.waitExit(); code != ExitUnpinned ||
		!strings.Contains(session.stderr.String(), "claude MCP client calls are not scripted here") {
		t.Fatalf("exit=%d stderr=%q", code, session.stderr.String())
	}
}
