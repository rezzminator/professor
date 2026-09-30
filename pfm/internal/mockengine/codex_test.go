package mockengine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless/run"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/inject"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/statusline"
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
	for _, args := range [][]string{{"mcp-server"}, {"app-server"}} {
		code, _, stderr := runOnce(fix, "codex", args, "")
		if code != ExitUnpinned || !strings.Contains(stderr, "codex "+args[0]) ||
			!strings.Contains(stderr, "unpinned") {
			t.Fatalf("codex %v exit=%d stderr=%q, want %d naming the door", args, code, stderr, ExitUnpinned)
		}
		if args[0] == "app-server" && !strings.Contains(stderr, "rate_limits") {
			t.Fatalf("unscripted app-server must name rate_limits: %q", stderr)
		}
	}
}

const fixtureRateLimits = `{"primary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":4102444800},"secondary":{"usedPercent":34,"windowDurationMins":10080,"resetsAt":4102444800},"planType":"plus"}`

func TestCodexAppServerFeedsPfmsStatuslineCache(t *testing.T) {
	fix := newFixture(t)
	setScenarioField(t, fix, "rate_limits", json.RawMessage(fixtureRateLimits))
	frames := "{\"jsonrpc\":\"2.0\",\"id\":0,\"method\":\"initialize\"}\n" +
		"{\"jsonrpc\":\"2.0\",\"method\":\"initialized\"}\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"account/rateLimits/read\"}\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"fixture/unknown\"}\n"
	code, stdout, stderr := runOnce(fix, "codex", []string{"app-server"}, frames)
	if code != 0 || stderr != "" {
		t.Fatalf("app-server EOF exit=%d stderr=%q", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("requests produced %d replies: %q", len(lines), stdout)
	}
	for i, line := range lines {
		var reply struct {
			ID     int
			Result struct{ RateLimits json.RawMessage }
			Error  struct {
				Code    int
				Message string
			}
		}
		if err := json.Unmarshal([]byte(line), &reply); err != nil {
			t.Fatal(err)
		}
		if reply.ID != i || (i == 1 && string(reply.Result.RateLimits) != fixtureRateLimits) ||
			(i == 2 && (reply.Error.Code != -32601 || !strings.Contains(reply.Error.Message, "fixture/unknown"))) {
			t.Fatalf("reply %d = %s", i, line)
		}
	}
	cache := filepath.Join(fix.root, "rate-limits.json")
	if err := statusline.RefreshCodex(context.Background(), statusline.CodexOptions{
		CachePath: cache, Binary: filepath.Join(fix.bin, "codex"),
	}); err != nil {
		t.Fatalf("pfm statusline refresh: %v", err)
	}
	var got struct {
		Primary struct {
			UsedPercent        int
			WindowDurationMins int
			ResetsAt           int64
		}
		Secondary struct {
			UsedPercent        int
			WindowDurationMins int
			ResetsAt           int64
		}
		PlanType string
	}
	if err := json.Unmarshal([]byte(readFile(t, cache)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Primary.UsedPercent != 12 || got.Primary.WindowDurationMins != 300 || got.Primary.ResetsAt != 4102444800 ||
		got.Secondary.UsedPercent != 34 || got.Secondary.WindowDurationMins != 10080 || got.PlanType != "plus" {
		t.Fatalf("pfm cache = %+v", got)
	}
}

func codexHeadlessRequest(fix *fixture, prompt string) run.Request {
	return run.Request{
		Config: pfmconfig.Config{
			Codex:         pfmconfig.CodexPrefs{Binary: "codex"},
			CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: fix.codexHome}},
		},
		Engine: pfmengine.Codex, Prompt: prompt, CWD: fix.work, Timeout: 20 * time.Second,
	}
}

func TestCodexExecIsReadByPfmsOwnRunner(t *testing.T) {
	for _, row := range []struct {
		name   string
		step   Step
		schema json.RawMessage
	}{
		{"answer", Step{Type: StepTurn, Reply: "forty-two", Tokens: &Tokens{Input: 11, Output: 4, CacheRead: 100, CacheCreation: 9}}, nil},
		{"structured", Step{Type: StepTurn, Structured: json.RawMessage(`{"ok":true}`)}, json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}`)},
		{"crash", Step{Type: StepCrash, ExitCode: 17}, nil},
		{"missing terminal", Step{Type: StepCrash}, nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			fix := newFixture(t)
			fix.write(Scenario{SessionID: fixtureThread, BusyMS: intPtr(0), Steps: []Step{row.step}})
			request := codexHeadlessRequest(fix, "what is the answer")
			request.Schema = row.schema
			result, err := run.Run(context.Background(), request)
			if row.step.Type == StepCrash {
				cause := "missing terminal success event"
				if row.step.ExitCode != 0 {
					cause = "exit status 17"
				}
				if err == nil || result.ExitCode != row.step.ExitCode || !strings.Contains(err.Error(), cause) ||
					!strings.Contains(result.Stdout, `"type":"turn.started"`) ||
					strings.Contains(result.Stdout, `"type":"turn.completed"`) {
					t.Fatalf("crash result=%+v err=%v", result, err)
				}
				return
			}
			if err != nil || result.ExitCode != 0 || result.IsError {
				t.Fatalf("headless result=%+v err=%v", result, err)
			}
			if row.schema != nil {
				var object map[string]bool
				if err := json.Unmarshal(
					result.StructuredOutput,
					&object,
				); err != nil || len(object) != 1 ||
					!object["ok"] {
					t.Fatalf("structured output=%s err=%v", result.StructuredOutput, err)
				}
			} else if result.Answer != "forty-two" || result.Usage == nil || result.Usage.Input != 11 || result.Usage.Output != 4 || result.Usage.CachedInput != 100 || result.Usage.CacheCreation != 9 {
				t.Fatalf("answer/usage=%+v", result)
			}
			if !strings.HasPrefix(result.Stdout, `{"thread_id":"`+fixtureThread+`","type":"thread.started"}`) &&
				!strings.HasPrefix(result.Stdout, `{"type":"thread.started","thread_id":"`+fixtureThread+`"}`) {
				t.Fatalf("first event=%q", result.Stdout)
			}
		})
	}
}

func TestCodexExecInlineAndStdinPrompts(t *testing.T) {
	for _, directive := range []string{`{"type":"turn","reply":"inline"}`, `{"type":"nope"}`} {
		for _, stdin := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stdin=%t", directive, stdin), func(t *testing.T) {
				fix := newFixture(t)
				fix.write(
					Scenario{
						SessionID: fixtureThread,
						BusyMS:    intPtr(0),
						Steps:     []Step{{Type: StepTurn, Reply: "positional"}},
					},
				)
				prompt := "query mock-engine: " + directive
				args, input := []string{"exec", "--json", "--ephemeral"}, ""
				if stdin {
					input = prompt
				} else {
					args = append(args, prompt)
				}
				code, stdout, stderr := runOnce(fix, "codex", args, input)
				if code != 0 {
					t.Fatalf("exec exit=%d stderr=%s", code, stderr)
				}
				var answer string
				for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
					var event struct{ Item struct{ Text string } }
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatal(err)
					}
					if event.Item.Text != "" {
						answer = event.Item.Text
					}
				}
				if (strings.Contains(directive, "nope") && !strings.HasPrefix(answer, "mock-engine: inline steps refused — ")) ||
					(!strings.Contains(directive, "nope") && answer != "inline") {
					t.Fatalf("answer=%q", answer)
				}
			})
		}
	}
}

func TestCodexExecPersistenceFollowsEphemeral(t *testing.T) {
	for _, ephemeral := range []bool{false, true} {
		t.Run(fmt.Sprint(ephemeral), func(t *testing.T) {
			fix := newFixture(t)
			fix.write(Scenario{SessionID: fixtureThread, BusyMS: intPtr(0), Reply: "stored reply"})
			request := codexHeadlessRequest(fix, "persist this exchange")
			request.NoSessionPersistence = ephemeral
			result, err := run.Run(context.Background(), request)
			if err != nil || result.Answer != "stored reply" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if ephemeral {
				for _, name := range []string{"sessions", codexmeta.SessionIndexFile} {
					if _, err := os.Stat(filepath.Join(fix.codexHome, name)); !os.IsNotExist(err) {
						t.Fatalf("ephemeral wrote %s: %v", name, err)
					}
				}
				return
			}
			rows, names := fix.indexedRollouts()
			if len(rows) != 1 || rows[0].ID != fixtureThread || rows[0].PromptCount != 1 ||
				names[fixtureThread] != "persist this exchange" {
				t.Fatalf("rollouts=%+v names=%v", rows, names)
			}
			meta, err := transcript.ReadMeta(fix.rolloutPath(), string(pfmengine.Codex))
			if err != nil || meta.Model != DefaultCodexModel || meta.ContextTokens != 15 {
				t.Fatalf("rollout meta=%+v err=%v", meta, err)
			}
		})
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

func TestCodexMCPStepCallsPfmsOwnServerWithPaneIdentity(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(fix.recordDir, "mcp-identity")
	command := fmt.Sprintf(`printf '%%s\n' "$TMUX" "$TMUX_PANE" "$CODEX_HOME" "$PWD" > %q; exec "$@"`, identity)
	writeCodexConfig(t, fix, fmt.Sprintf("[mcp_servers.professor]\ncommand = \"sh\"\nargs = [%q,%q,%q,%q,%q]\n",
		"-c", command, "fixture", executable, verbsStdioProfessorArg))
	fix.write(Scenario{SessionID: fixtureThread, Pane: codexShapes, BusyMS: intPtr(0)})
	// chat_find searches Claude transcripts; its fixture must seed that store.
	code, _, stderr := runOnce(
		fix,
		"claude",
		claudeArgs("--session-id", fixtureSession, "-p", "violet archive fixture"),
		"",
	)
	if code != 0 {
		t.Fatalf("seed Claude transcript: exit=%d stderr=%q", code, stderr)
	}
	session := fix.startTUI(
		"codex",
		codexArgs(),
		map[string]string{"TMUX": "/fixture/cx-fixture,42,0", "TMUX_PANE": "%8"},
	)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "›") })
	session.typeLine("violet archive fixture")
	session.waitFrame("seed reply", func(frame string) bool { return strings.Contains(frame, "⏺ ok") })
	fix.indexedRollouts()
	session.typeLine(
		`query mock-engine: [{"type":"mcp","tool":"chat_find","input":{"excerpt":"violet archive fixture","include_self":true}},{"type":"turn","reply":"MCP finished"}]`,
	)
	session.waitFrame("MCP reply", func(frame string) bool { return strings.Contains(frame, "⏺ MCP finished") })
	result := readFile(t, filepath.Join(fix.recordDir, "mcp-call-chat_find.json"))
	if !strings.Contains(result, fixtureSession) || strings.Contains(result, `"isError":true`) {
		t.Fatalf("pfm tools/call=%s", result)
	}
	var callID string
	var outputs int
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, fix.rolloutPath())), "\n") {
		var payload struct {
			Type   string
			Name   string
			CallID string `json:"call_id"`
			Output string
		}
		var envelope struct{ Payload json.RawMessage }
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Type == "function_call" && payload.Name == "chat_find" {
			callID = payload.CallID
		}
		if payload.Type == "function_call_output" && payload.CallID == callID &&
			strings.Contains(payload.Output, fixtureSession) {
			outputs++
		}
	}
	if callID == "" || outputs != 1 {
		t.Fatalf("MCP rollout call=%q outputs=%d", callID, outputs)
	}
	if got := readFile(t, identity); got != "/fixture/cx-fixture,42,0\n%8\n"+fix.codexHome+"\n"+fix.work+"\n" {
		t.Fatalf("MCP child identity=%q", got)
	}
}

func TestCodexMCPFailuresAreRecordedShownAndKeepPaneLive(t *testing.T) {
	for _, row := range []struct{ name, table, tool, cause string }{
		{"missing server", "", "chat_find", "professor"},
		{"call", "configured", "unknown_fixture_tool", "tools/call"},
	} {
		t.Run(row.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			table := row.table
			if table == "configured" {
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				table = fmt.Sprintf(
					"[mcp_servers.professor]\ncommand = %q\nargs = [%q]\n",
					executable,
					verbsStdioProfessorArg,
				)
			}
			writeCodexConfig(t, fix, table)
			fix.write(
				Scenario{
					SessionID: fixtureThread,
					Pane:      codexShapes,
					BusyMS:    intPtr(0),
					Steps:     []Step{{Type: StepMCP, Tool: row.tool}},
				},
			)
			session := fix.startTUI("codex", codexArgs(), nil)
			session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "›") })
			session.typeLine("call the tool")
			session.waitFrame("failed tool and completed turn", func(frame string) bool {
				return strings.Contains(frame, "⏺ ok") && strings.Contains(frame, row.cause)
			})
			result := readFile(t, filepath.Join(fix.recordDir, "mcp-call-"+row.tool+".json"))
			if !strings.Contains(result, `"isError":true`) || !strings.Contains(result, row.cause) {
				t.Fatalf("failed result=%s", result)
			}
			if content := readFile(
				t,
				fix.rolloutPath(),
			); !strings.Contains(content, "function_call_output") ||
				!strings.Contains(content, row.cause) {
				t.Fatalf("failed rollout=%s", content)
			}
			session.typeLine("still alive")
			waitFile(
				t,
				fix.rolloutPath(),
				func(content string) bool { return strings.Contains(content, "still alive") },
			)
		})
	}
}

func TestCodexInlineTurnsKeepPositionalCursor(t *testing.T) {
	fix := newFixture(t)
	fix.write(
		Scenario{
			SessionID: fixtureThread,
			Pane:      codexShapes,
			BusyMS:    intPtr(0),
			Steps:     []Step{{Type: StepTurn, Reply: "positional"}},
		},
	)
	session := fix.startTUI("codex", codexArgs(), nil)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "›") })
	session.typeLine(`query mock-engine: {"type":"turn","reply":"inline"}`)
	session.waitFrame("inline reply", func(frame string) bool { return strings.Contains(frame, "⏺ inline") })
	session.typeLine("ordinary query")
	session.waitFrame("positional reply", func(frame string) bool { return strings.Contains(frame, "⏺ positional") })
	if content := readFile(
		t,
		fix.rolloutPath(),
	); !strings.Contains(content, "mock-engine:") ||
		!strings.Contains(content, "ordinary query") {
		t.Fatalf("user records=%s", content)
	}
}

func TestCodexCompactWritesReceiptAndRollout(t *testing.T) {
	fix := newFixture(t)
	shapes := codexShapes
	shapes.Busy += " · 1s"
	fix.write(Scenario{SessionID: fixtureThread, Pane: shapes, Steps: []Step{{Type: StepCompact, BusyMS: 180}}})
	session := fix.startTUI("codex", codexArgs(), nil)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "›") })
	session.typeLine("/compact")
	session.waitFrame("compaction busy footer", func(frame string) bool {
		return inject.IsBusy(frame) && strings.Contains(frame, codexShapes.Busy)
	})
	if frame := session.out.frame(); strings.Contains(frame, codexShapes.Compacted) {
		t.Fatalf("receipt painted while compaction busy: %s", frame)
	}
	if content := readFile(t, fix.rolloutPath()); strings.Contains(content, `"type":"compacted"`) {
		t.Fatalf("compaction record written before busy footer cleared: %s", content)
	}
	session.waitFrame(
		"compaction receipt",
		func(frame string) bool {
			return strings.Contains(frame, codexShapes.Compacted) && !inject.IsBusy(frame)
		},
	)
	if content := readFile(
		t,
		fix.rolloutPath(),
	); !strings.Contains(content, `"type":"compacted"`) ||
		!strings.Contains(content, "replacement_history") {
		t.Fatalf("compaction record=%s", content)
	}
}

func TestCodexCompactEscapeClearsFooterWithoutReceipt(t *testing.T) {
	fix := newFixture(t)
	shapes := codexShapes
	shapes.Busy += " · 1s"
	fix.write(Scenario{SessionID: fixtureThread, Pane: shapes, Steps: []Step{{Type: StepCompact, BusyMS: 1500}}})
	session := fix.startTUI("codex", codexArgs(), nil)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "›") })
	session.typeLine("/compact")
	session.waitFrame("compaction busy footer", inject.IsBusy)
	session.typeRaw("\x1b")
	session.waitFrame(
		"cleared footer",
		func(frame string) bool { return !inject.IsBusy(frame) && strings.Contains(frame, "›") },
	)
	if frame := session.out.frame(); strings.Contains(frame, codexShapes.Compacted) {
		t.Fatalf("interrupted compaction painted receipt: %s", frame)
	}
	if content := readFile(t, fix.rolloutPath()); strings.Contains(content, `"type":"compacted"`) {
		t.Fatalf("interrupted compaction wrote record: %s", content)
	}
}

func TestCodexCompactBusyDuration(t *testing.T) {
	session := &codexSession{}
	for _, tc := range []struct {
		step Step
		want time.Duration
	}{
		{Step{Type: StepCompact, BusyMS: 125}, 125 * time.Millisecond},
		{Step{Type: StepCompact}, 5 * time.Second},
	} {
		if got := session.compactBusyDuration(tc.step); got != tc.want {
			t.Fatalf("compactBusyDuration(%+v) = %s, want %s", tc.step, got, tc.want)
		}
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

func rolloutHandles(t *testing.T, path string) int {
	t.Helper()
	const fdRoot = "/proc/self/fd"
	entries, err := os.ReadDir(fdRoot)
	if err != nil {
		t.Fatalf("enumerate rollout handles: %v", err)
	}
	count := 0
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join(fdRoot, entry.Name()))
		if os.IsNotExist(err) {
			continue // ReadDir's own descriptor has already closed.
		}
		if err != nil {
			t.Fatalf("read descriptor %s: %v", entry.Name(), err)
		}
		if target == path {
			count++
		}
	}
	return count
}

func TestCodexRolloutHandleLivesForTheSession(t *testing.T) {
	fix := newFixture(t)
	proc := &process{
		ctx: context.Background(), engine: engineCodex, env: fix.env(nil), cwd: fix.work,
		pid: os.Getpid(), stderr: io.Discard, script: newScript(Scenario{}, engineCodex),
	}
	for _, resumed := range []bool{false, true} {
		t.Run(fmt.Sprintf("resumed=%t", resumed), func(t *testing.T) {
			session := &codexSession{proc: proc, codexHome: fix.codexHome, threadID: fixtureThread, model: "fixture"}
			if err := session.start(resumed); err != nil {
				t.Fatal(err)
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

func TestCodexStatusFooterKeepsMCPFailureOnItsOwnLine(t *testing.T) {
	fix := newFixture(t)
	session := &codexSession{
		proc: &process{cwd: fix.work}, codexHome: fix.codexHome, threadID: fixtureThread,
		model: "fixture-model", mcpError: "MCP fixture failed",
	}
	for _, name := range []string{"", "named thread"} {
		if name != "" {
			if err := session.rename(name); err != nil {
				t.Fatal(err)
			}
		}
		footer, err := session.statusLine(Tokens{})
		label := name
		if label == "" {
			label = session.model
		}
		if want := label + " · " + fix.work + "\r\n" + session.mcpError; err != nil || footer != want {
			t.Fatalf("footer = %q, err=%v, want %q", footer, err, want)
		}
	}
}
