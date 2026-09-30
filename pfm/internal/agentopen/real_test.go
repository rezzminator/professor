package agentopen

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestExecCommandsQueriesAndViewsWithoutRecording(t *testing.T) {
	commands, argvPath, values := testExecCommands(t)
	ctx := context.Background()
	if output, err := commands.QueryAgents(ctx, "/account/2"); err != nil || string(output) != "[]\n" {
		t.Fatalf("query output=%q error=%v", output, err)
	}
	query := assertAgentLaunch(t, argvPath, "agents", "--json")
	if len(query.Hooks) != 0 || query.MCPConfig != "" || query.Autonomy {
		t.Fatalf("query carried session-only settings: %+v", query)
	}
	if err := commands.View(ctx, "/account/2", "/project"); err != nil {
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

func TestExecCommandsResumeRecordsDirectLaunch(t *testing.T) {
	for _, configKey := range []bool{false, true} {
		name := "environment"
		if configKey {
			name = "config key"
		}
		t.Run(name, func(t *testing.T) {
			commands, argvPath, values := testExecCommands(t)
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
			if err := commands.Resume(ctx, "/account/2", t.TempDir(), id, true); err != nil {
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

func testExecCommands(t *testing.T) (ExecCommands, string, paths.Values) {
	t.Helper()
	root := t.TempDir()
	argvPath := filepath.Join(root, "argv")
	binary := filepath.Join(root, "claude")
	if err := os.WriteFile(
		binary,
		[]byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$AGENTOPEN_ARGV\"\n"+
			"printf '%s\\n' \"${CLAUDE_CODE_PROMPT_CACHE_TTL-unset}\" > \"$AGENTOPEN_ARGV.ttl\"\nprintf '[]\\n'\n"),
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
		Accounts: []config.Account{{ID: 2, ConfigDir: "/account/2"}},
	}
	return ExecCommands{Home: root, Machine: machine}, argvPath, values
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
		t.Fatalf("rendered argv=%q parsed=%+v process CLAUDE_CODE_PROMPT_CACHE_TTL=%q", argv, parsed, ttl)
	}
	return parsed
}

// fakeAgentopenTmuxBinary plays tmux: list-panes answers a fixed pid.
func fakeAgentopenTmuxBinary(t *testing.T, pid int) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\nprintf '" + strconv.Itoa(pid) + "\\n'\nexit 0\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
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
