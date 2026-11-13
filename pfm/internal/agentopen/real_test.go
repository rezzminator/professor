package agentopen

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

func TestExecCommandsQueriesAndViewsWithoutRecording(t *testing.T) {
	commands, runner, values, accountDir := testExecCommands(t)
	ctx := context.Background()
	if output, err := commands.QueryAgents(ctx, accountDir); err != nil || string(output) != "[]\n" {
		t.Fatalf("query output=%q error=%v", output, err)
	}
	query := assertAgentLaunch(t, runner, "agents", "--json")
	if len(query.Hooks) != 0 || query.MCPConfig != "" || query.Autonomy {
		t.Fatalf("query carried session-only settings: %+v", query)
	}
	if err := commands.View(ctx, accountDir, "/project"); err != nil {
		t.Fatal(err)
	}
	view := assertAgentLaunch(t, runner, "agents", "--cwd", "/project")
	if len(view.Hooks) != 0 || view.MCPConfig != "" || view.Autonomy {
		t.Fatalf("view carried session-only settings: %+v", view)
	}
	if _, err := os.Stat(values.StateDB); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("query/view created a launch database: %v", err)
	}
}

func TestExecCommandsResumeWorkbench(t *testing.T) {
	for _, test := range []struct {
		name, manifest string
		wantPersona    bool
		wantEffort     string
		// wantWarning is what the resume tells its stderr, dir and manifest
		// substituted for {dir} and {manifest}.
		wantWarning string
	}{
		{
			name: "valid", manifest: `{"prompt":"scribe.md","effort":"xhigh","model":"sonnet"}`,
			wantPersona: true, wantEffort: "xhigh",
		},
		{
			name: "unknown effort", manifest: `{"prompt":"scribe.md","effort":"turbo","model":"sonnet"}`,
			wantPersona: true,
			wantWarning: `pfm: workbench {dir}: unknown Claude effort "turbo" (want low, medium, high, xhigh or max); ` +
				"launching the persona on the default effort\n",
		},
		{
			name: "invalid manifest", manifest: `{"prompt":""}`,
			wantWarning: `pfm: workbench persona not applied ({manifest}: "prompt" is required); launching without it` + "\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			commands, runner, values, accountDir := testExecCommands(t)
			var stderr bytes.Buffer
			commands.Stderr = &stderr
			dir := filepath.Join(values.Home, "acme", "docs", "scribe")
			prompt := filepath.Join(dir, ".professor", "scribe.md")
			files := map[string]string{
				filepath.Join(values.Home, "acme", ".professor", "baseline.json"): "{}",
				prompt: "You are scribe.",
			}
			files[paths.WorkbenchManifest(dir)] = test.manifest
			for path, body := range files {
				if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			const id = "44444444-4444-4444-8444-444444444444"
			if err := commands.Resume(context.Background(), accountDir, dir, id, true); err != nil {
				t.Fatalf("resume refused: %v", err)
			}
			wantWarning := strings.NewReplacer("{dir}", dir, "{manifest}", paths.WorkbenchManifest(dir)).
				Replace(test.wantWarning)
			if stderr.String() != wantWarning {
				t.Fatalf("resume stderr = %q, want %q", stderr.String(), wantWarning)
			}
			parsed := assertAgentLaunch(t, runner, "--resume", id)
			wantPrompt, wantModel := "", ""
			if test.wantPersona {
				wantPrompt, wantModel = prompt, "sonnet"
			}
			if parsed.Resume != id || parsed.PromptFile != wantPrompt || parsed.Effort != test.wantEffort ||
				parsed.Model != wantModel {
				t.Fatalf("persona = %+v, want prompt %q effort %q model %q",
					parsed, wantPrompt, test.wantEffort, wantModel)
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
			commands, runner, values, accountDir := testExecCommands(t)
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
			assertAgentLaunch(t, runner, "--resume", id)
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

func TestExecCommandsResumeWarnsWhenTheFleetPromptCannotReachTheChat(t *testing.T) {
	const id = "66666666-6666-4666-8666-666666666666"
	const want = "Claude started WITHOUT its system prompt file"
	for _, test := range []struct {
		name    string
		policy  string
		stage   bool
		warning bool
		// terminal: stdin and stderr are a terminal; wait: held for Enter before Claude starts.
		// The pane is always a terminal, attached or not: the door that attaches
		// holds, so the pane only prints, or a detached open would never start
		// and an injected message would be eaten as the Enter.
		terminal, wait bool
	}{
		{"professor policy with no source clone", config.SystemPromptProfessor, false, true, true, false},
		{"professor policy with no source clone off a terminal", config.SystemPromptProfessor, false, true, false, false},
		{"professor policy with the composed prompt staged", config.SystemPromptProfessor, true, false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := strings.NewReader("\n")
			t.Cleanup(launchnotice.Replace(input, test.terminal))
			commands, _, _, accountDir := testExecCommands(t)
			commands.Machine.Claude.SystemPrompt = test.policy
			if test.stage {
				if err := paths.WriteSourceRepoMarker(commands.Home, t.TempDir()); err != nil {
					t.Fatal(err)
				}
				promptPath, err := action.ProfessorPromptPath(commands.Home)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(promptPath, []byte("professor prompt\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var stderr bytes.Buffer
			commands.Stderr = &stderr
			if err := commands.Resume(context.Background(), accountDir, t.TempDir(), id, false); err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(stderr.String(), want); got != test.warning {
				t.Fatalf("stderr warns=%t, want %t: %q", got, test.warning, stderr.String())
			}
			if waited := input.Len() == 0; waited != test.wait {
				t.Fatalf("read Enter = %t, want %t: %q", waited, test.wait, stderr.String())
			}
		})
	}
}

func testExecCommands(t *testing.T) (ExecCommands, *deps.FakeRunner, paths.Values, string) {
	t.Helper()
	root := t.TempDir()
	accountDir := filepath.Join(root, "account", "2")
	if err := os.MkdirAll(accountDir, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "claude")
	if err := testjail.WriteExecutableNeverRun(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &deps.FakeRunner{}
	runner.ScriptStartOutput(nil, deps.StartOutput{Stdout: "[]\n"})
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
	return ExecCommands{Home: root, Machine: machine, Runner: runner}, runner, values, accountDir
}

// assertAgentLaunch parses the argv of the last Claude child the runner started, checks the leading
// arguments appear in it and returns what it parsed.
func assertAgentLaunch(t *testing.T, runner *deps.FakeRunner, leading ...string) claudelaunch.Parsed {
	t.Helper()
	starts := runner.Starts()
	if len(starts) == 0 {
		t.Fatal("no Claude child was started")
	}
	last := starts[len(starts)-1]
	argv := last.Argv[1:]
	content := strings.Join(argv, "\n") + "\n"
	parsed, err := claudelaunch.Parse(append([]string{"claude"}, argv...))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range leading {
		if !strings.Contains(content, value+"\n") {
			t.Fatalf("argv=%q lacks %q", argv, value)
		}
	}
	ttl := "unset"
	for _, entry := range last.Opts.Env {
		if value, found := strings.CutPrefix(entry, "CACHE_LIVE_CONTROL_MAIN_TTL="); found {
			ttl = value
		}
	}
	if parsed.Settings["outputStyle"] != "default" ||
		strings.TrimSpace(ttl) != "1h" && parsed.Resume != "" {
		t.Fatalf("rendered argv=%q parsed=%+v process CACHE_LIVE_CONTROL_MAIN_TTL=%q", argv, parsed, ttl)
	}
	return parsed
}

// fakeAgentopenTmux plays tmux: list-panes answers one pane, in gather's
// pane format, running a fixed pid.
func fakeAgentopenTmux(pid int) *deps.FakeRunner {
	fields := []string{
		"cc-1",
		"@1",
		"win",
		"title",
		"/dev/pts/7",
		strconv.Itoa(pid),
		"%1",
		"1",
		"/work",
		"claude",
		"0",
		"0",
	}
	return testjail.TmuxRunner(deps.RunResult{Stdout: []byte(strings.Join(fields, "\x1f") + "\n")})
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
	tmux := RealTmux{Binary: "tmux", Dir: dir}
	socket, err := tmux.SocketForPID(pfmtmux.WithClientRunner(ctx, fakeAgentopenTmux(4242)), 4242)
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

// A tmux-resident chat is never attached inside a Herdr pane: the refusal
// comes before tmux runs at all.
func TestRealTmuxAttachRefusesInHerdr(t *testing.T) {
	ctx, recorder := obs.Test(t)
	t.Setenv("HERDR_PANE_ID", "w1:p2")
	tmux := RealTmux{Binary: "tmux", Dir: t.TempDir()}
	err := tmux.Attach(pfmtmux.WithClientRunner(ctx, fakeAgentopenTmux(4242)), "cc-1-1-1")
	want := "host refused at agent open: this terminal is Herdr pane w1:p2, where a tmux client is never attached; " +
		"open the chat from a plain terminal"
	if err == nil || err.Error() != want || len(recorder.Records()) != 0 {
		t.Fatalf("attach = %v, tmux records %s; want %q and no tmux run", err, recorder.Raw(), want)
	}
}
