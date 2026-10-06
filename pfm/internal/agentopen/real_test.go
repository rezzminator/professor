package agentopen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestExecCommandsQueriesAndViewsWithoutRecording(t *testing.T) {
	commands, argvPath, values, accountDir := testExecCommands(t)
	ctx := context.Background()
	if output, err := commands.QueryAgents(ctx, accountDir); err != nil || string(output) != "[]\n" {
		t.Fatalf("query output=%q error=%v", output, err)
	}
	query := assertAgentLaunch(t, argvPath, "agents", "--json")
	if len(query.Hooks) != 0 || query.MCPConfig != "" || query.Autonomy {
		t.Fatalf("query carried session-only settings: %+v", query)
	}
	if err := commands.View(ctx, accountDir, "/project"); err != nil {
		t.Fatal(err)
	}
	view := assertAgentLaunch(t, argvPath, "agents", "--cwd", "/project")
	if len(view.Hooks) != 0 || view.MCPConfig != "" || view.Autonomy {
		t.Fatalf("view carried session-only settings: %+v", view)
	}
	if _, err := os.Stat(values.StateDB); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("query/view created a launch database: %v", err)
	}
}

func TestExecCommandsResumeWorkbench(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprintf("invalid=%v", invalid), func(t *testing.T) {
			commands, argvPath, values, accountDir := testExecCommands(t)
			dir := filepath.Join(values.Home, "acme", "docs", "scribe")
			prompt := filepath.Join(dir, ".professor", "scribe.md")
			manifest := `{"prompt":"scribe.md","effort":"xhigh","model":"sonnet"}`
			if invalid {
				manifest = `{"prompt":""}`
			}
			for path, body := range map[string]string{filepath.Join(values.Home, "acme", ".professor", "baseline.json"): "{}", paths.WorkbenchManifest(dir): manifest, prompt: "You are scribe."} {
				if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			const id = "44444444-4444-4444-8444-444444444444"
			err := commands.Resume(context.Background(), accountDir, dir, id, true)
			if invalid {
				want := "resume agent session: " + paths.WorkbenchManifest(dir) + `: "prompt" is required`
				if err == nil || err.Error() != want {
					t.Fatalf("resume error = %v, want %s", err, want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			parsed := assertAgentLaunch(t, argvPath, "--resume", id)
			if parsed.Resume != id || parsed.PromptFile != prompt || parsed.Effort != "xhigh" ||
				parsed.Model != "sonnet" {
				t.Fatalf("persona = %+v", parsed)
			}
		})
	}
}

func TestExecCommandsResumeRecordsDirectLaunch(t *testing.T) {
	for _, configKey := range []bool{false, true} {
		name := "environment"
		if configKey {
			name = "config key"
		}
		t.Run(name, func(t *testing.T) {
			commands, argvPath, values, accountDir := testExecCommands(t)
			if configKey {
				configPath := filepath.Join(values.Home, "pfm.config.json")
				content := []byte(`{"version":2,"state":{"db":"` + values.StateDB + `"}}`)
				if err := os.WriteFile(configPath, content, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv(paths.EnvConfig, configPath)
				t.Setenv(paths.EnvStateDB, "")
				t.Setenv(paths.EnvCacheDB, "")
			}
			ctx := context.Background()
			const id = "33333333-3333-4333-8333-333333333333"
			if err := commands.Resume(ctx, accountDir, t.TempDir(), id, true); err != nil {
				t.Fatal(err)
			}
			assertAgentLaunch(t, argvPath, "--resume", id)
			launches, err := fleetdb.OpenLaunches(ctx, values)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := launches.Close(); err != nil {
					t.Error(err)
				}
			}()
			record, err := launches.LaunchFor(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if record.SessionID != id || record.Engine != pfmengine.Claude || record.Account != 2 || !record.Cache1H {
				t.Fatalf("resume record=%+v", record)
			}
		})
	}
}

func testExecCommands(t *testing.T) (ExecCommands, string, paths.Values, string) {
	t.Helper()
	root := t.TempDir()
	accountDir := filepath.Join(root, "account", "2")
	if err := os.MkdirAll(accountDir, 0o700); err != nil {
		t.Fatal(err)
	}
	argvPath := filepath.Join(root, "argv")
	binary := filepath.Join(root, "claude")
	if err := testjail.WriteExecutable(
		binary,
		[]byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$AGENTOPEN_ARGV\"\n"+
			"printf '%s\\n' \"${CACHE_LIVE_CONTROL_MAIN_TTL-unset}\" > \"$AGENTOPEN_ARGV.ttl\"\nprintf '[]\\n'\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTOPEN_ARGV", argvPath)
	t.Setenv(paths.EnvHome, root)
	t.Setenv(paths.EnvStateDB, filepath.Join(root, "state", "pfm.db"))
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	machine := config.Config{
		Claude:   config.ClaudePrefs{Binary: binary},
		Accounts: []config.Account{{ID: 2, ConfigDir: accountDir}},
	}
	return ExecCommands{Home: root, Machine: machine}, argvPath, values, accountDir
}

func assertAgentLaunch(t *testing.T, argvPath string, leading ...string) claudelaunch.Parsed {
	t.Helper()
	content, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	parsed, err := claudelaunch.Parse(append([]string{"claude"}, argv...))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range leading {
		if !strings.Contains(string(content), value+"\n") {
			t.Fatalf("argv=%q lacks %q", argv, value)
		}
	}
	ttl, err := os.ReadFile(argvPath + ".ttl")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Settings["outputStyle"] != "default" ||
		strings.TrimSpace(string(ttl)) != "1h" && parsed.Resume != "" {
		t.Fatalf("rendered argv=%q parsed=%+v process CACHE_LIVE_CONTROL_MAIN_TTL=%q", argv, parsed, ttl)
	}
	return parsed
}

// fakeAgentopenTmuxBinary plays tmux: list-panes answers a fixed pid.
func fakeAgentopenTmuxBinary(t *testing.T, pid int) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\nprintf '" + strconv.Itoa(pid) + "\\n'\nexit 0\n"
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

// TestRealTmuxRecordsEveryInvocation proves RealTmux.command crosses the
// observed tmux door (pfmtmux.Exec, not the bare pfmtmux.Command) — pattern:
// internal/kill/tmux_test.go.
func TestRealTmuxRecordsEveryInvocation(t *testing.T) {
	ctx, recorder := obs.Test(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cc-1-1-1"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tmux := RealTmux{Binary: fakeAgentopenTmuxBinary(t, 4242), Dir: dir}
	socket, err := tmux.SocketForPID(ctx, 4242)
	if err != nil {
		t.Fatal(err)
	}
	if socket != "cc-1-1-1" {
		t.Fatalf("socket = %q, want cc-1-1-1", socket)
	}
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: %s", len(records), recorder.Raw())
	}
	if records[0].Message != "tmux.exec" {
		t.Fatalf("record = %s, want tmux.exec", records[0].Message)
	}
	if comp, _ := records[0].Field(obs.FieldComp); comp != "tmux" {
		t.Fatalf("comp = %v, want tmux", comp)
	}
	if subcmd, _ := records[0].Field("subcmd"); subcmd != "list-panes" {
		t.Fatalf("subcmd = %v, want list-panes", subcmd)
	}
	_ = context.Background
}

// L1-F10: tmux never starting (a configured binary that does not exist) must
// surface as an error, not be folded into "this socket has no such pane" —
// the split resolve.Resolver already makes via pfmtmux.CouldNotRun.
func TestSocketForPIDErrorsWhenTmuxCouldNotRun(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "cc-dead-one"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tmux := RealTmux{Binary: filepath.Join(t.TempDir(), "absent", "tmux"), Dir: directory}
	socket, err := tmux.SocketForPID(context.Background(), 46)
	if err == nil {
		t.Fatalf("SocketForPID() with a missing tmux binary returned no error; socket=%q", socket)
	}
	if socket != "" {
		t.Fatalf("SocketForPID() socket = %q, want empty on a tmux-could-not-run error", socket)
	}
}

// TestExecCommandsResumeCarriesTheChatLabel pins the agent-open resume door:
// the resumed chat is launched under its pfm label, and an unindexed session
// under none.
func TestExecCommandsResumeCarriesTheChatLabel(t *testing.T) {
	commands, argvPath, _, accountDir := testExecCommands(t)
	ctx := context.Background()
	const named, unknown = "44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555"
	database, err := store.Open(store.WithWarningWriter(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertTranscript(ctx, store.Transcript{
		UUID: named, Path: "/fixtures/named.jsonl", CustomTitle: "Fix login", FirstPrompt: "first",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{named: "Fix login", unknown: ""} {
		if err := commands.Resume(ctx, accountDir, t.TempDir(), id, true); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(argvPath)
		if err != nil {
			t.Fatal(err)
		}
		argv := strings.Split(strings.TrimSpace(string(raw)), "\n")
		got := ""
		for index, word := range argv {
			if word == "--name" && index+1 < len(argv) {
				got = argv[index+1]
			}
		}
		if got != want {
			t.Fatalf("resume %s --name = %q, want %q; argv %q", id, got, want, argv)
		}
	}
}
