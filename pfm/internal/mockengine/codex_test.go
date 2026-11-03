package mockengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless/run"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
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

func codexHeadlessRequest(fix *fixture, prompt string) run.Request {
	if err := os.WriteFile(
		filepath.Join(fix.codexHome, "auth.json"),
		[]byte(`{"tokens":{"access_token":"fixture","account_id":"fixture"}}`),
		0o600,
	); err != nil {
		fix.t.Fatal(err)
	}
	return run.Request{
		Config: pfmconfig.Config{
			Codex:         pfmconfig.CodexPrefs{Binary: "codex"},
			CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: fix.codexHome}},
		},
		Engine: pfmengine.Codex, Prompt: prompt, CWD: fix.work, Timeout: 20 * time.Second,
	}
}

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
	professor, err := mcpserv.NewFamilyServers(mcpserv.FamilyServerOptions{Version: "test", Chat: service})
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
	writeCodexConfig(t, fix, fmt.Sprintf("[mcp_servers.chat-professor]\ncommand = %q\nargs = [%q]\n",
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
	recorded := waitFile(t, filepath.Join(fix.recordDir, "mcp-chat-professor.json"), func(content string) bool {
		return strings.Contains(content, "whoami")
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
	writeCodexConfig(t, fix, fmt.Sprintf("[mcp_servers.chat-professor]\ncommand = \"sh\"\nargs = [%q,%q,%q,%q,%q]\n",
		"-c", command, "fixture", executable, verbsStdioProfessorArg))
	fix.write(
		Scenario{
			SessionID: fixtureThread,
			Pane:      codexShapes,
			BusyMS:    intPtr(0),
			Steps:     []Step{{Type: StepTurn}, {Type: StepTurn}},
		},
	)
	// Use a completed Claude exchange so last has a stable answer while
	// this Codex turn exercises the protocol and its pane identity.
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
		fmt.Sprintf(
			`query mock-engine: [{"type":"mcp","tool":"last","input":{"target":%q}},{"type":"turn","reply":"MCP finished"}]`,
			fixtureSession,
		),
	)
	session.waitFrame("MCP reply", func(frame string) bool { return strings.Contains(frame, "⏺ MCP finished") })
	result := readFile(t, filepath.Join(fix.recordDir, "mcp-call-last.json"))
	if !strings.Contains(result, fixtureSession) || !strings.Contains(result, `"text":"ok"`) ||
		strings.Contains(result, `"isError":true`) {
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
		if payload.Type == "function_call" && payload.Name == "last" {
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
		{"missing server", "", "last", "professor"},
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
					"[mcp_servers.chat-professor]\ncommand = %q\nargs = [%q]\n",
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
					Steps:     []Step{{Type: StepMCP, Tool: row.tool}, {Type: StepTurn}, {Type: StepTurn}},
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
		return pfmcall.IsBusy(frame) && strings.Contains(frame, codexShapes.Busy)
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
			return strings.Contains(frame, codexShapes.Compacted) && !pfmcall.IsBusy(frame)
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
	session.waitFrame("compaction busy footer", pfmcall.IsBusy)
	session.typeRaw("\x1b")
	session.waitFrame(
		"cleared footer",
		func(frame string) bool { return !pfmcall.IsBusy(frame) && strings.Contains(frame, "›") },
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
	writeCodexConfig(t, fix, "[mcp_servers.chat-professor]\nurl = \"http://127.0.0.1:1/mcp/chat-professor\"\n")
	fix.write(Scenario{SessionID: fixtureThread, Pane: codexShapes, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepMCP},
	}})
	session := fix.startTUI("codex", codexArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "›") })
	session.typeLine("list the fleet tools")
	if code := session.waitExit(); code != ExitUnpinned ||
		!strings.Contains(session.stderr.String(), "has no [mcp_servers.chat-professor] command") {
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
	const fdRoot = "/dev/fd" // Linux and macOS both list this process's descriptors here
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat rollout %s: %v", path, err)
	}
	rollout := info.Sys().(*syscall.Stat_t)
	entries, err := os.ReadDir(fdRoot)
	if err != nil {
		t.Fatalf("enumerate rollout handles: %v", err)
	}
	count := 0
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil {
			t.Fatalf("descriptor name %q in %s: %v", entry.Name(), fdRoot, err)
		}
		// Fstat, never stat of /dev/fd/N: macOS's fdesc answers that stat with its own node.
		var st syscall.Stat_t
		if err := syscall.Fstat(fd, &st); errors.Is(err, syscall.EBADF) {
			continue // ReadDir's own descriptor has already closed.
		} else if err != nil {
			t.Fatalf("fstat descriptor %d: %v", fd, err)
		}
		if st.Dev == rollout.Dev && st.Ino == rollout.Ino {
			count++
		}
	}
	return count
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
