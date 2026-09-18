package mockengine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/headless/run"
	"hostops/pfm/internal/index"
	"hostops/pfm/internal/inject"
	"hostops/pfm/internal/store"
	"hostops/pfm/internal/transcript"
)

const fixtureSession = "b1111111-1111-4111-8111-111111111111"

// claudeArgs is the argv pfm's spawn template hands a Claude chat
// (internal/action/claude_spawn.go): the caller's words, then LaunchArgs.
func claudeArgs(extra ...string) []string {
	return append(extra, "--settings", `{"outputStyle":"default"}`, "--model", "claude-sonnet-4-5-fixture")
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
