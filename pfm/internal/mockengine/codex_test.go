package mockengine

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/codexappendix"
	"hostops/pfm/internal/codexmeta"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/index"
	"hostops/pfm/internal/mcpserv"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/store"
	"hostops/pfm/internal/transcript"
)

const fixtureThread = "019ff700-0000-7000-8000-000000000001"

// codexShapes are FIXTURE strings, not Codex's spelling: the real pane text is
// UNPINNED (testdata/shapes/codex/*.txt) and pfm matches none of it, so these
// tests only prove the mock renders what the scenario supplies.
var codexShapes = PaneShapes{Busy: "FIXTURE-CODEX-BUSY", Compacted: "FIXTURE-CODEX-COMPACTED"}

// codexArgs is pfm's Codex launch argv (internal/action, absent config).
func codexArgs(extra ...string) []string {
	return append([]string{"--dangerously-bypass-approvals-and-sandbox"}, extra...)
}

// rolloutPath finds the one rollout the mock wrote under the Codex home.
func (fix *fixture) rolloutPath() string {
	fix.t.Helper()
	var found string
	err := filepath.WalkDir(
		filepath.Join(fix.codexHome, "sessions"),
		func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasPrefix(entry.Name(), "rollout-") {
				found = path
			}
			return nil
		},
	)
	if err != nil || found == "" {
		fix.t.Fatalf("no rollout under %s (err=%v)", fix.codexHome, err)
	}
	return found
}

func (fix *fixture) indexedRollouts() ([]store.Rollout, map[string]string) {
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
	if err := index.SyncCodex(ctx, database, []string{fix.codexHome}, &counters); err != nil {
		fix.t.Fatalf("index Codex rollouts: %v", err)
	}
	if counters.FilesSeen == 0 {
		fix.t.Fatalf("the indexer saw no rollout under %s", fix.codexHome)
	}
	rows, err := database.Rollouts(ctx)
	if err != nil {
		fix.t.Fatal(err)
	}
	names, err := database.CxNames(ctx)
	if err != nil {
		fix.t.Fatal(err)
	}
	return rows, names
}

func TestCodexTUIRefusesToRenderWithoutPaneShapes(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureThread})
	session := fix.startTUI("codex", codexArgs(), nil)
	if code := session.waitExit(); code != ExitUnpinned {
		t.Fatalf("exit = %d, want %d", code, ExitUnpinned)
	}
	want := "mock-engine: codex pane shapes are unpinned — supply pane.busy and pane.compacted in the scenario"
	if !strings.Contains(session.stderr.String(), want) {
		t.Fatalf("stderr = %q, want %q", session.stderr.String(), want)
	}
	if _, err := os.Stat(filepath.Join(fix.codexHome, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("a refused TUI still wrote a rollout (stat err=%v)", err)
	}
}

func TestCodexHeadlessDoorsAreRefusedByName(t *testing.T) {
	fix := newFixture(t)
	for _, args := range [][]string{{"exec", "--json", "hello"}, {"app-server"}} {
		code, _, stderr := runOnce(fix, "codex", args, "")
		if code != ExitUnpinned || !strings.Contains(stderr, "codex "+args[0]) ||
			!strings.Contains(stderr, "unpinned") {
			t.Fatalf("codex %v exit=%d stderr=%q, want %d naming the door", args, code, stderr, ExitUnpinned)
		}
	}
}

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

// stageAppendix writes the Professor appendix where codexappendix.Run reads it.
func (fix *fixture) stageAppendix() {
	fix.t.Helper()
	path := codexappendix.PromptPath(fix.home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fix.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("Fixture appendix body.\n"), 0o600); err != nil {
		fix.t.Fatal(err)
	}
}

// appendixAnswer runs pfm's own appendix hook over a payload and returns its
// stdout — the answer a real `pfm internal codex-appendix` would print.
func (fix *fixture) appendixAnswer(payload string) string {
	fix.t.Helper()
	var out bytes.Buffer
	if err := codexappendix.Run(strings.NewReader(payload), &out, fix.home); err != nil {
		fix.t.Fatalf("codexappendix.Run(%s): %v", payload, err)
	}
	return out.String()
}

func TestCodexSessionStartHookIsAnsweredByPfmsAppendixAndLandsInHistory(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.stageAppendix()
	// The hook command replays what pfm's handler answers to a fresh thread;
	// the recorder keeps the payload the mock sent it.
	answer := fix.appendixAnswer(`{"hook_event_name":"SessionStart","source":"startup"}`)
	hook := filepath.Join(fix.root, "appendix-hook.sh")
	if err := os.WriteFile(
		hook,
		[]byte("#!/bin/sh\ncat >> \""+fix.recordDir+"/CodexSessionStart.jsonl\"\nprintf '%s' '"+
			strings.ReplaceAll(strings.TrimSpace(answer), "'", `'"'"'`)+"'\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	document := map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{
		"matcher": codexappendix.Matcher,
		"hooks":   []any{map[string]any{"type": "command", "command": hook, "timeout": 10}},
	}}}}
	content, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fix.codexHome, "hooks.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	fix.write(Scenario{SessionID: fixtureThread, Pane: codexShapes, BusyMS: intPtr(0)})
	session := fix.startTUI("codex", codexArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "›") })
	payload := fix.waitRecorded("CodexSessionStart", 1)[0]
	var request struct {
		Event      string `json:"hook_event_name"`
		Source     string `json:"source"`
		Transcript string `json:"transcript_path"`
	}
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		t.Fatal(err)
	}
	rollout := fix.rolloutPath()
	if request.Event != "SessionStart" || request.Source != "startup" || request.Transcript != rollout {
		t.Fatalf("appendix hook payload = %+v, want SessionStart/startup naming %s", request, rollout)
	}
	session.typeLine("first")
	session.waitFrame("the reply", func(frame string) bool { return strings.Contains(frame, DefaultReply) })
	// pfm's handler over the REAL rollout: the appendix the mock injected as a
	// developer message is found, so nothing is re-injected and no warning is
	// raised — the only reader of the history shape the mock wrote.
	settled := fix.appendixAnswer(
		`{"hook_event_name":"SessionStart","source":"resume","transcript_path":"` + rollout + `"}`,
	)
	if strings.TrimSpace(settled) != "{}" {
		t.Fatalf("codexappendix.Run over the mock's rollout = %s, want {} (appendix present, no warning)", settled)
	}
	session.typeLine("/compact")
	session.waitFrame("the supplied compacted shape", func(frame string) bool {
		return strings.Contains(frame, codexShapes.Compacted)
	})
	compacted := fix.appendixAnswer(
		`{"hook_event_name":"SessionStart","source":"compact","transcript_path":"` + rollout + `"}`,
	)
	if strings.Contains(compacted, "systemMessage") || !strings.Contains(compacted, "additionalContext") {
		t.Fatalf(
			"after compaction codexappendix.Run = %s, want a clean re-injection (replacement_history read, no warning)",
			compacted,
		)
	}
}

func TestCodexMCPStepHandshakesWithPfmsOwnServer(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	service, err := mcpserv.NewConfigured("test", os.Stderr, mcpserv.Runtime{Paths: resolved})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	server := httptest.NewServer(service.NewHTTPHandler())
	defer server.Close()
	config := "# fixture\n# BEGIN pfm mcp_servers — installer-owned\n[mcp_servers.chat]\nurl = \"" +
		server.URL + "/mcp/chat\"\n# END pfm mcp_servers — installer-owned\n"
	if err := os.WriteFile(filepath.Join(fix.codexHome, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	fix.write(Scenario{SessionID: fixtureThread, Pane: codexShapes, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepMCP, Server: "chat"},
		{Type: StepTurn, Reply: "tools listed"},
	}})
	session := fix.startTUI("codex", codexArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "›") })
	session.typeLine("list the fleet tools")
	session.waitFrame("the reply after the handshake", func(frame string) bool {
		return strings.Contains(frame, "tools listed")
	})
	recorded := waitFile(t, filepath.Join(fix.recordDir, "mcp-chat.json"), func(content string) bool {
		return strings.Contains(content, "chat_whoami")
	})
	var tools []string
	if err := json.Unmarshal([]byte(recorded), &tools); err != nil {
		t.Fatal(err)
	}
	if want := mcpserv.ToolNames(); strings.Join(tools, ",") != strings.Join(want, ",") {
		t.Fatalf("tools/list from pfm's server = %v, want %v", tools, want)
	}
}
