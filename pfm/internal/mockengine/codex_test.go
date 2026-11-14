package mockengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless/run"
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
	for door, want := range map[string]string{"mcp-server": "mock-engine: codex mcp-server JSON-RPC is unpinned in this mock\n", "app-server": "mock-engine: codex app-server JSON-RPC is unpinned — supply rate_limits in the scenario\n"} {
		code, _, stderr := runOnce(fix, "codex", []string{door}, "")
		if got := strings.ReplaceAll(stderr, fix.root, "{root}"); code != ExitUnpinned || got != want {
			t.Fatalf("codex %s exit %d stderr = %q, want %q", door, code, got, want)
		}
	}
}

const fixtureRateLimits = `{"primary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":4102444800},"secondary":{"usedPercent":34,"windowDurationMins":10080,"resetsAt":4102444800},"planType":"plus"}`

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
	if got, want := jsonRecords(
		t,
		readFile(t, fix.rolloutPath()),
		"role",
		"user",
		"content",
	), "[{\"text\":\"query mock-engine: {\\\"type\\\":\\\"turn\\\",\\\"reply\\\":\\\"inline\\\"}\",\"type\":\"input_text\"}]\n[{\"text\":\"ordinary query\",\"type\":\"input_text\"}]"; got != want {
		t.Fatalf("user records = %q, want %q", got, want)
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
	if got, want := recordTypes(t, readFile(t, fix.rolloutPath())), "session_meta turn_context"; got != want {
		t.Fatalf("records while compaction is busy = %q, want %q", got, want)
	}
	session.waitFrame(
		"compaction receipt",
		func(frame string) bool {
			return strings.Contains(frame, codexShapes.Compacted) && !pfmcall.IsBusy(frame)
		},
	)
	if got, want := jsonRecords(
		t,
		readFile(t, fix.rolloutPath()),
		"type",
		"compacted",
		"payload",
	), `{"message":"Summary of the thread so far (fixture).","replacement_history":[{"content":[{"text":"Summary of the thread so far (fixture).","type":"input_text"}],"role":"user","type":"message"}]}`; got != want {
		t.Fatalf("compaction record = %q, want %q", got, want)
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
	if got, want := recordTypes(t, readFile(t, fix.rolloutPath())), "session_meta turn_context"; got != want {
		t.Fatalf("records after an interrupted compaction = %q, want %q", got, want)
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
	code := session.waitExit()
	if got, want := strings.ReplaceAll(
		session.stderr.String(),
		fix.root,
		"{root}",
	), "mock-engine: mcp: {root}/codex/config.toml has no [mcp_servers.chat-professor] command\n"; code != ExitUnpinned ||
		got != want {
		t.Fatalf("exit %d stderr = %q, want %q", code, got, want)
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
