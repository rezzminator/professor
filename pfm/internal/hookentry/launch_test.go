package hookentry

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestLaunchPassThroughPredicate(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		arguments []string
		tmux      string
		forced    bool
		want      bool
	}{
		{name: "interactive empty argv"},
		{name: "resume stays interactive", arguments: []string{"--resume", "session-id"}},
		{name: "print short", arguments: []string{"-p", "hello"}, want: true},
		{name: "print long anywhere", arguments: []string{"--model", "opus", "--print", "hello"}, want: true},
		{name: "output format separate", arguments: []string{"--output-format", "json"}, want: true},
		{name: "output format equals", arguments: []string{"--output-format=stream-json"}, want: true},
		{name: "help short", arguments: []string{"-h"}, want: true},
		{name: "help long", arguments: []string{"--help"}, want: true},
		{name: "version short", arguments: []string{"-v"}, want: true},
		{name: "version long", arguments: []string{"--version"}, want: true},
		{name: "agents subcommand", arguments: []string{"agents", "--json"}, want: true},
		{name: "subcommand after flags", arguments: []string{"--verbose", "doctor"}, want: true},
		{name: "option value is first nonflag", arguments: []string{"--model", "opus", "agents"}},
		{name: "ordinary prompt", arguments: []string{"hello"}},
		{name: "already in claude socket", tmux: "/tmp/tmux-1000/cc-1-2-3,123,0", want: true},
		{name: "already in codex socket", tmux: "/tmp/tmux-1000/cx-1-2-3,123,0", want: true},
		{name: "unmanaged tmux", tmux: "/tmp/tmux-1000/vsct,123,0"},
		{name: "forced", forced: true, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := launchPassThrough(test.arguments, test.tmux, test.forced); got != test.want {
				t.Fatalf(
					"launchPassThrough(%q, %q, %t) = %t, want %t",
					test.arguments,
					test.tmux,
					test.forced,
					got,
					test.want,
				)
			}
		})
	}
}

func TestLaunchWorkbench(t *testing.T) {
	for _, scenario := range []string{"new", "resume", "explicit", "invalid", "disabled", "disabled session id", "disabled resume", "outside", "version", "mcp"} {
		t.Run(scenario, func(t *testing.T) {
			root := testjail.ShortRoot(t)
			dir := filepath.Join(root, "acme", "docs", "scribe")
			prompt := filepath.Join(dir, ".professor", "scribe.md")
			manifest := `{"prompt":"scribe.md","effort":"xhigh"}`
			args := []string{}
			wantPrompt, wantEffort, wantError := prompt, "xhigh", ""
			switch scenario {
			case "resume":
				args = []string{"--resume", "44444444-4444-4444-8444-444444444444"}
			case "explicit":
				args = []string{"--effort", "low", "--system-prompt-file", "/work/alt.md"}
				wantPrompt, wantEffort = "/work/alt.md", "low"
			case "invalid":
				manifest = `{"prompt":""}`
				wantError = "pfm internal launch: " + paths.WorkbenchManifest(dir) + `: "prompt" is required` + "\n"
			case "disabled", "disabled session id", "disabled resume":
				manifest = `{"prompt":"scribe.md","engines":["codex"],"effort":"xhigh"}`
				if scenario == "disabled session id" {
					// --session-id names a NEW session: a disabled engine is refused.
					args = []string{"--session-id", "55555555-5555-4555-8555-555555555555"}
				}
				if scenario != "disabled resume" {
					wantError = "pfm internal launch: workbench " + dir + ` does not enable claude: add "claude" to "engines" in ` + paths.WorkbenchManifest(
						dir,
					) + "\n"
				} else {
					args = []string{"--continue"}
					wantPrompt, wantEffort = "", ""
				}
			case "outside":
				wantPrompt, wantEffort = "", ""
			case "version", "mcp":
				manifest = `{"prompt":""}`
				args = []string{"--version"}
				if scenario == "mcp" {
					args = []string{"mcp", "list"}
				}
			}
			for path, body := range map[string]string{
				filepath.Join(root, "acme", ".professor", "baseline.json"): "{}",
				paths.WorkbenchManifest(dir):                               manifest, prompt: "You are scribe.",
			} {
				if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "outside" {
				dir = filepath.Join(root, "acme", "src")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			binary, argvPath := filepath.Join(root, "claude"), filepath.Join(root, "argv")
			if err := testjail.WriteExecutable(
				binary,
				[]byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+action.Quote(argvPath)+"\n"),
				0o700,
			); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("TMUX", "")
			t.Setenv(paths.EnvHome, root)
			t.Setenv(paths.EnvStateDB, filepath.Join(root, "state", "pfm.db"))
			runtime := config.Runtime{
				Config: config.Config{},
				Paths: paths.Values{
					Home:    root,
					StateDB: filepath.Join(root, "state", "pfm.db"),
					SIDDir:  filepath.Join(root, "sid"),
					TmuxDir: filepath.Join(root, "tmux"),
				},
			}
			var stderr, stdout bytes.Buffer
			previous := LaunchExec
			t.Cleanup(func() { LaunchExec = previous })
			LaunchExec = func(path string, argv, _ []string) error {
				if scenario != "version" && scenario != "mcp" {
					t.Fatal("refusal reached exec")
				}
				if path != binary || !reflect.DeepEqual(argv, append([]string{binary}, args...)) {
					t.Fatalf("passthrough = %s %q", path, argv)
				}
				return nil
			}
			// A non-terminal launcher runs the real tmux boundary and records the engine argv.
			code := Launch(
				append([]string{"--real", binary, "--cwd", dir, "--"}, args...),
				&stdout,
				&stderr,
				runtime,
				&paths.MapEnv{},
			)
			if wantError != "" {
				if code != 1 || stderr.String() != wantError {
					t.Fatalf("refusal = %d %q, want 1 %q", code, stderr.String(), wantError)
				}
				return
			}
			if code != 0 {
				t.Fatalf("launch = %d: %s", code, stderr.String())
			}
			if scenario == "version" || scenario == "mcp" {
				return
			}
			raw, err := os.ReadFile(argvPath)
			if err != nil {
				t.Fatal(err)
			}
			argv := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
			parsed, err := claudelaunch.Parse(append([]string{binary}, argv...))
			if err != nil {
				t.Fatal(err)
			}
			if parsed.PromptFile != wantPrompt || parsed.Effort != wantEffort {
				t.Fatalf(
					"persona = %q %q, want %q %q; argv %q",
					parsed.PromptFile,
					parsed.Effort,
					wantPrompt,
					wantEffort,
					argv,
				)
			}
			if scenario == "explicit" {
				for _, flag := range []string{"--effort", "--system-prompt-file"} {
					count := 0
					for _, arg := range argv {
						if arg == flag {
							count++
						}
					}
					if count != 1 {
						t.Errorf("%s count = %d", flag, count)
					}
				}
			}
		})
	}
}

func TestLaunchWarnsWhenTheFleetPromptCannotReachTheChat(t *testing.T) {
	const want = "Claude started WITHOUT its system prompt file"
	for _, test := range []struct {
		name    string
		policy  string
		stage   bool
		refused bool // the chat's tmux server cannot be created: no chat starts
		warning bool
		// terminal: stdin and stderr are a terminal; wait: the launcher holds for Enter.
		terminal, wait bool
		// cancel: the starter presses Ctrl-C at the Enter prompt; the launch is
		// abandoned and the server it already made is killed, not orphaned.
		cancel bool
	}{
		{"professor policy with no source clone", config.SystemPromptProfessor, false, false, true, true, true, false},
		{"professor policy with no source clone off a terminal", config.SystemPromptProfessor, false, false, true, false, false, false},
		{"professor policy with the composed prompt staged", config.SystemPromptProfessor, true, false, false, true, false, false},
		{"production policy needs no prompt", config.SystemPromptProduction, false, false, false, true, false, false},
		{"professor policy with no source clone, server refused", config.SystemPromptProfessor, false, true, false, true, false, false},
		{"professor policy with no source clone, Ctrl-C at the prompt", config.SystemPromptProfessor, false, false, true, true, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := testjail.ShortRoot(t)
			dir := filepath.Join(root, "acme", "src")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := atomicfile.Write(
				filepath.Join(root, "acme", ".professor", "baseline.json"), []byte("{}"), 0o600,
			); err != nil {
				t.Fatal(err)
			}
			if test.stage {
				if err := paths.WriteSourceRepoMarker(root, t.TempDir()); err != nil {
					t.Fatal(err)
				}
				promptPath, err := action.ProfessorPromptPath(root)
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
			binary, argvPath := filepath.Join(root, "claude"), filepath.Join(root, "argv")
			if err := testjail.WriteExecutable(
				binary,
				[]byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+action.Quote(argvPath)+"\n"),
				0o700,
			); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("TMUX", "")
			t.Setenv(paths.EnvHome, root)
			t.Setenv(paths.EnvStateDB, filepath.Join(root, "state", "pfm.db"))
			runtime := config.Runtime{
				Config: config.Config{Claude: config.ClaudePrefs{SystemPrompt: test.policy}},
				Paths: paths.Values{
					Home:    root,
					StateDB: filepath.Join(root, "state", "pfm.db"),
					SIDDir:  filepath.Join(root, "sid"),
					TmuxDir: filepath.Join(root, "tmux"),
				},
			}
			if test.refused {
				if err := os.WriteFile(runtime.Paths.TmuxDir, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stderr, stdout bytes.Buffer
			enter := strings.NewReader("\n")
			var input io.Reader = enter
			if test.cancel {
				t.Setenv(spawn.TestFreshSocketEnv, "cc-cancel")
				// The test process outlives the interrupt whatever the launcher does.
				guard := make(chan os.Signal, 1)
				signal.Notify(guard, os.Interrupt)
				t.Cleanup(func() { signal.Stop(guard) })
				released := make(chan struct{})
				t.Cleanup(func() { close(released) })
				input = interruptingReader{released: released}
			}
			t.Cleanup(launchnotice.Replace(input, test.terminal))
			code := Launch(
				[]string{"--real", binary, "--cwd", dir, "--"}, &stdout, &stderr, runtime, &paths.MapEnv{},
			)
			if test.cancel {
				if code != 130 {
					t.Fatalf("launch cancelled at the prompt = %d, want 130: %s", code, stderr.String())
				}
				socket := filepath.Join(runtime.Paths.TmuxDir, "cc-cancel")
				if exec.Command("tmux", "-S", socket, "has-session").Run() == nil {
					_ = exec.Command("tmux", "-S", socket, "kill-server").Run()
					t.Fatalf("the cancelled launch left its tmux server %s running", socket)
				}
			} else if waited := enter.Len() == 0; waited != test.wait {
				t.Fatalf("read Enter = %t, want %t: %q", waited, test.wait, stderr.String())
			} else if (code != 0) != test.refused {
				t.Fatalf("launch = %d, want refused=%t: %s", code, test.refused, stderr.String())
			}
			if got := strings.Contains(stderr.String(), want); got != test.warning {
				t.Fatalf("stderr warns=%t, want %t: %q", got, test.warning, stderr.String())
			}
		})
	}
}

// interruptingReader is a starter pressing Ctrl-C at the Enter prompt: its read
// interrupts the process, then waits for a line that never comes, giving up
// after a bound so an interrupt nobody handles fails the test, not hangs it.
type interruptingReader struct{ released chan struct{} }

func (reader interruptingReader) Read([]byte) (int, error) {
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		return 0, err
	}
	select {
	case <-reader.released:
	case <-time.After(5 * time.Second):
	}
	return 0, io.EOF
}

func TestLaunchPassthroughSessionEnvironment(t *testing.T) {
	accountDir := t.TempDir()
	wantNames := claudelaunch.SessionEnv(config.ClaudePrefs{AutoCompactWindow: 50000})
	for _, entry := range wantNames {
		name, _, _ := strings.Cut(entry, "=")
		t.Setenv(name, "inherited")
	}
	t.Setenv("PFM_KEEP", "kept")
	previousExec := LaunchExec
	t.Cleanup(func() { LaunchExec = previousExec })
	machine := config.Runtime{Config: config.Config{
		Claude: config.ClaudePrefs{AutoCompactWindow: 100000},
		Accounts: []config.Account{
			{
				ID:        2,
				ConfigDir: accountDir,
				Claude:    &config.ClaudePrefs{AutoCompactWindow: 50000, HelperModel: "claude-sonnet-5-5"},
			},
		},
	}}
	for _, test := range []struct {
		name, ambient, tmux, forced string
		args                        []string
		wantWindow                  int64
		wantHelper                  string
		session                     bool
	}{
		{"print account override", accountDir + "/.", "", "", []string{"-p", "hi"}, 50000, "claude-sonnet-5-5", true},
		{"forced resume", accountDir, "", "1", []string{"--resume", "X"}, 50000, "claude-sonnet-5-5", true},
		{"pfm socket", accountDir, "/tmp/tmux-1000/cc-1-2-3,123,0", "", []string{"--resume", "X"}, 50000, "claude-sonnet-5-5", true},
		{"unmatched ambient", "", "", "", []string{"-p", "hi"}, 100000, "", true},
		{"plugin", accountDir, "", "", []string{"plugin", "install", "x"}, 0, "", false},
		{"mcp", accountDir, "", "", []string{"mcp", "list"}, 0, "", false},
		{"config", accountDir, "", "", []string{"config"}, 0, "", false},
		{"agents query", accountDir, "", "", []string{"agents", "--json"}, 0, "", false},
		{"version", accountDir, "", "", []string{"--version"}, 0, "", false},
		{"short version", accountDir, "", "", []string{"-v"}, 0, "", false},
		{"help", accountDir, "", "", []string{"-h"}, 0, "", false},
		{"long help", accountDir, "", "", []string{"--help"}, 0, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", test.ambient)
			t.Setenv("TMUX", test.tmux)
			t.Setenv("PFM_LAUNCH_PASSTHROUGH", test.forced)
			inherited := os.Environ()
			var got []string
			LaunchExec = func(_ string, _, environment []string) error {
				got = append([]string(nil), environment...)
				return nil
			}
			var stderr bytes.Buffer
			if code := Launch(
				append([]string{"--real", "/bin/echo", "--"}, test.args...),
				&bytes.Buffer{},
				&stderr,
				machine,
				paths.OSEnv{},
			); code != 0 {
				t.Fatalf("Launch code=%d stderr=%q", code, stderr.String())
			}
			if !test.session {
				// Only the re-entry marker joins an otherwise untouched environment.
				want := append(append([]string(nil), inherited...), "PFM_CLAUDE_LAUNCH_PID="+strconv.Itoa(os.Getpid()))
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("non-session env changed: got=%q want=%q", got, want)
				}
				return
			}
			for _, want := range claudelaunch.SessionEnv(config.ClaudePrefs{AutoCompactWindow: test.wantWindow, HelperModel: test.wantHelper}) {
				if count := countEnvironmentEntry(got, want); count != 1 {
					t.Errorf("%q occurs %d times in exec env", want, count)
				}
				name, _, _ := strings.Cut(want, "=")
				if count := countEnvironmentEntry(got, name+"=inherited"); count != 0 {
					t.Errorf("inherited %q survived %d times in exec env", name, count)
				}
			}
			// The helper model is the account's, else Haiku 5.5, whatever the shell exported.
			wantHelper := test.wantHelper
			if wantHelper == "" {
				wantHelper = "claude-haiku-5-5"
			}
			if want := "ANTHROPIC_SMALL_FAST_MODEL=" + wantHelper; countEnvironmentEntry(got, want) != 1 {
				t.Errorf("%q occurs %d times in exec env, want once", want, countEnvironmentEntry(got, want))
			}
			if countEnvironmentEntry(got, "PFM_KEEP=kept") != 1 {
				t.Errorf("unrelated env lost from exec: %q", got)
			}
		})
	}
}

func countEnvironmentEntry(environment []string, want string) int {
	count := 0
	for _, entry := range environment {
		if entry == want {
			count++
		}
	}
	return count
}

func TestReadLaunchStatusRejectsMissingAndInvalidFiles(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "status")
	_, err := readLaunchStatus(path)
	if err == nil || !strings.Contains(err.Error(), "launcher status file missing") {
		t.Fatalf("readLaunchStatus missing error=%v", err)
	}
	if err := os.WriteFile(path, []byte("not-a-status\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = readLaunchStatus(path)
	if err == nil || !strings.Contains(err.Error(), "launcher status file invalid") {
		t.Fatalf("readLaunchStatus invalid error=%v", err)
	}
}

func TestAttachArgumentsBindNestedSeatToItsPane(t *testing.T) {
	t.Parallel()
	base := []string{"tmux", "-S", "/s/cc-1", "wait-for", "-S", "start", ";", "attach-session", "-t", "cc-1"}
	if got := attachArguments("/s/cc-1", "start", "cc-1", false); !reflect.DeepEqual(got, base) {
		t.Fatalf("plain terminal: attachArguments = %q, want %q", got, base)
	}
	nested := append(append([]string{}, base...), ";", "set-option", "-t", "cc-1", "destroy-unattached", "on")
	if got := attachArguments("/s/cc-1", "start", "cc-1", true); !reflect.DeepEqual(got, nested) {
		t.Fatalf("nested tmux: attachArguments = %q, want %q", got, nested)
	}
}

// TestLauncherSeatAccountIgnoresTheLoginDefault: a bare `claude` from a shell
// carrying the login default lands on the fleet primary, as with nothing
// exported; an explicit CLAUDE_CONFIG_DIR still picks its own account.
func TestLauncherSeatAccountIgnoresTheLoginDefault(t *testing.T) {
	one, two := t.TempDir(), t.TempDir()
	machine := config.Runtime{Config: config.Config{Accounts: []config.Account{
		{ID: 1, ConfigDir: one}, {ID: 2, ConfigDir: two},
	}}}
	for _, test := range []struct {
		name, value, sentinel, wantDir string
		wantAccount                    int
	}{
		{"neither set", "", "", two, 2},
		{"login default", one, one, two, 2},
		{"explicit, no sentinel", one, "", one, 1},
		{"explicit over another dir's sentinel", one, two, one, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := &paths.MapEnv{Values: map[string]string{
				"CLAUDE_CONFIG_DIR": test.value, claudelaunch.ConfigDirDefaultEnv: test.sentinel,
			}}
			dir, account := launcherSeatAccount(machine, 2, env)
			if dir != test.wantDir || account != test.wantAccount {
				t.Fatalf("launcherSeatAccount = (%s, %d), want (%s, %d)", dir, account, test.wantDir, test.wantAccount)
			}
		})
	}
}

func TestLaunchPassthroughWaitsForParentAccountRegistration(t *testing.T) {
	account := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", account)
	t.Setenv("PFM_LAUNCH_PASSTHROUGH", "1")
	parent, err := gather.AcquireAccountGuard(account, true)
	if err != nil {
		t.Fatal(err)
	}
	previous := LaunchExec
	t.Cleanup(func() { LaunchExec = previous })
	LaunchExec = func(string, []string, []string) error { return nil }
	// The parent publishes its child's generation and releases ownership while
	// the managed child launcher is starting, before that child execs Claude.
	released := make(chan error, 1)
	time.AfterFunc(20*time.Millisecond, func() { released <- parent.Close() })
	var out, stderr bytes.Buffer
	code := Launch(
		[]string{"--real", "/bin/echo", "--", "--resume", "fixture-session"},
		&out,
		&stderr,
		config.Runtime{},
		paths.OSEnv{},
	)
	if err := <-released; err != nil {
		t.Fatal(err)
	}
	if err := parent.Abort(); err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("parent registration rejected healthy managed child: code=%d stderr=%q", code, stderr.String())
	}
}

func TestLaunchPassthroughDecidesTheCostLedgerOfASessionOnly(t *testing.T) {
	previousExec, previousArm := LaunchExec, armCostLedger
	t.Cleanup(func() { LaunchExec, armCostLedger = previousExec, previousArm })
	t.Setenv("PFM_LAUNCH_PASSTHROUGH", "1")
	t.Setenv("TMUX", "")
	for _, test := range []struct {
		name      string
		args      []string
		ledger    bool
		wantCalls int
	}{
		{"interactive resume with the ledger on", []string{"--resume", "X"}, true, 1},
		{"interactive start with the ledger switched off", []string{"--model", "opus"}, false, 1},
		{"print mode is no chat", []string{"-p", "hi"}, true, 0},
		{"output-format is print mode", []string{"--output-format=json", "hi"}, true, 0},
		{"a subcommand starts no session", []string{"mcp", "list"}, true, 0},
		{"help starts no session", []string{"--help"}, true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var enabled []bool
			armCostLedger = func(environ []string, _ paths.Env, ledger bool, _ io.Writer) ([]string, string) {
				enabled = append(enabled, ledger)
				return append(append([]string(nil), environ...), "PFM_COST_LEDGER=armed-by-test"), "on"
			}
			var execEnv []string
			LaunchExec = func(_ string, _, environment []string) error {
				execEnv = append([]string(nil), environment...)
				return nil
			}
			machine := config.Runtime{Config: config.Config{Claude: config.ClaudePrefs{CostLedger: test.ledger}}}
			if code := Launch(
				append([]string{"--real", "/bin/echo", "--"}, test.args...),
				&bytes.Buffer{}, &bytes.Buffer{}, machine, paths.OSEnv{},
			); code != 0 {
				t.Fatalf("Launch code = %d", code)
			}
			if len(enabled) != test.wantCalls {
				t.Fatalf("the ledger was decided %d times, want %d", len(enabled), test.wantCalls)
			}
			if test.wantCalls == 1 {
				if enabled[0] != test.ledger {
					t.Errorf("decided with claude.costLedger=%t, want %t", enabled[0], test.ledger)
				}
				if countEnvironmentEntry(execEnv, "PFM_COST_LEDGER=armed-by-test") != 1 {
					t.Errorf("the exec environment lacks the ledger decision: %q", execEnv)
				}
			} else if countEnvironmentEntry(execEnv, "PFM_COST_LEDGER=armed-by-test") != 0 {
				t.Errorf("a run that starts no chat carries a ledger decision: %q", execEnv)
			}
		})
	}
}
