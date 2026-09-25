package mockengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/transcript"
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

// stdioProfessorArg re-executes this test binary as the stdio server a Codex
// config.toml names: a chat-only professor server over RunStdio, no daemon.
const stdioProfessorArg = "mockengine-stdio-professor"

func init() {
	if len(os.Args) < 2 || os.Args[1] != stdioProfessorArg {
		return
	}
	os.Exit(serveStdioProfessor())
}

func serveStdioProfessor() int {
	resolved, err := paths.Resolve()
	if err != nil {
		fmt.Fprintf(os.Stderr, "stdio professor: resolve paths: %v\n", err)
		return 1
	}
	service, err := mcpserv.NewConfigured("test", os.Stderr, mcpserv.Runtime{Paths: resolved})
	if err != nil {
		fmt.Fprintf(os.Stderr, "stdio professor: configure chat: %v\n", err)
		return 1
	}
	defer func() {
		if err := service.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "stdio professor: close chat: %v\n", err)
		}
	}()
	professor, err := mcpserv.NewProfessor(mcpserv.ProfessorOptions{Version: "test", Chat: service})
	if err != nil {
		fmt.Fprintf(os.Stderr, "stdio professor: %v\n", err)
		return 1
	}
	if err := professor.RunStdio(context.Background(), os.Stdin, os.Stdout, mcpserv.StdioOptions{
		Home: resolved.Home, SIDDir: resolved.SIDDir, Warnings: os.Stderr,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "stdio professor: %v\n", err)
		return 1
	}
	return 0
}

func writeCodexConfig(t *testing.T, fix *fixture, table string) {
	t.Helper()
	config := "# fixture\n# BEGIN pfm mcp_servers — installer-owned\n" + table +
		"# END pfm mcp_servers — installer-owned\n"
	if err := os.WriteFile(filepath.Join(fix.codexHome, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexMCPStepHandshakesWithPfmsOwnServer(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	writeCodexConfig(t, fix, fmt.Sprintf("[mcp_servers.professor]\ncommand = %q\nargs = [%q]\n",
		executable, stdioProfessorArg))
	fix.write(Scenario{SessionID: fixtureThread, Pane: codexShapes, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepMCP},
		{Type: StepTurn, Reply: "tools listed"},
	}})
	session := fix.startTUI("codex", codexArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "›") })
	session.typeLine("list the fleet tools")
	session.waitFrame("the reply after the handshake", func(frame string) bool {
		return strings.Contains(frame, "tools listed")
	})
	recorded := waitFile(t, filepath.Join(fix.recordDir, "mcp-professor.json"), func(content string) bool {
		return strings.Contains(content, "chat_whoami")
	})
	var tools []string
	if err := json.Unmarshal([]byte(recorded), &tools); err != nil {
		t.Fatal(err)
	}
	if want := mcpserv.ToolNames(); strings.Join(tools, ",") != strings.Join(want, ",") {
		t.Fatalf("tools/list from pfm's stdio server = %v, want %v", tools, want)
	}
}

func TestCodexMCPStepNamesATableWithoutCommand(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	writeCodexConfig(t, fix, "[mcp_servers.professor]\nurl = \"http://127.0.0.1:1/mcp/professor\"\n")
	fix.write(Scenario{SessionID: fixtureThread, Pane: codexShapes, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepMCP},
	}})
	session := fix.startTUI("codex", codexArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "›") })
	session.typeLine("list the fleet tools")
	if code := session.waitExit(); code != ExitUnpinned ||
		!strings.Contains(session.stderr.String(), "has no [mcp_servers.professor] command") {
		t.Fatalf("exit=%d stderr=%q, want %d naming the table without a command",
			code, session.stderr.String(), ExitUnpinned)
	}
}

// TestMCPBoundedContextAppliesTheConfiguredTimeout covers F4: session.mcp's
// context.WithTimeout must actually bound the handshake with the spawned stdio
// server — verified at the context itself rather than over a real hung child.
// Without mcpBoundedContext, the mock would hand Connect/ListTools the
// caller's own context — which carries no deadline of its own — leaving a
// server that never answers free to hang the mock forever exactly as F4 named.
func TestMCPBoundedContextAppliesTheConfiguredTimeout(t *testing.T) {
	previous := mcpTimeout
	mcpTimeout = 250 * time.Millisecond
	t.Cleanup(func() { mcpTimeout = previous })
	bounded, cancel := mcpBoundedContext(context.Background())
	defer cancel()
	deadline, ok := bounded.Deadline()
	if !ok {
		t.Fatal("mcpBoundedContext returned a context with no deadline — a stdio server that never answers " +
			"would then have nothing bounding it")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > mcpTimeout {
		t.Fatalf("deadline is %s from now, want within (0, %s]", remaining, mcpTimeout)
	}
}
