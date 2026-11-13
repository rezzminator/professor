package hookentry

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
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
			if got := launchPassThrough(
				test.arguments,
				resolve.Identity{Backend: "tmux", SocketPath: pfmtmux.SocketPath(test.tmux)},
				test.forced,
			); got != test.want {
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

// interruptingReader is a starter pressing Ctrl-C at the Enter prompt: its read
// interrupts the process, then waits for a line that never comes, giving up
// after a bound so an interrupt nobody handles fails the test, not hangs it.
type interruptingReader struct {
	released chan struct{}
	finished chan struct{}
}

func (reader interruptingReader) Read([]byte) (int, error) {
	defer close(reader.finished)
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
			if test.tmux != "" {
				proveLauncherTmux(t)
			}
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
	_, missing := readLaunchStatus(path)
	if err := os.WriteFile(path, []byte("not-a-status\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, invalid := readLaunchStatus(path)
	if got, want := strings.ReplaceAll(
		fmt.Sprint(missing, " | ", invalid),
		path,
		"{status}",
	), "launcher status file missing: open {status}: no such file or directory | launcher status file invalid: parse=strconv.Atoi: parsing \"not-a-status\": invalid syntax value=\"not-a-status\""; got != want {
		t.Errorf("missing | invalid = %q, want %q", got, want)
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

// A print, help or non-interactive-subcommand run is never the chat bound to
// the parent's Herdr pane, so its environment loses the four Herdr names; the
// forced pass-through and the one inside a pfm socket are the launches of a
// chat that owns a pane, and keep them.
func TestLaunchPassthroughDropsTheHerdrPaneOfAHeadlessRunOnly(t *testing.T) {
	previousExec, previousArm := LaunchExec, armCostLedger
	t.Cleanup(func() { LaunchExec, armCostLedger = previousExec, previousArm })
	armCostLedger = func(environ []string, _ paths.Env, _ bool, _ io.Writer) ([]string, string) { return environ, "off" }
	herdrEnv := []string{
		"HERDR_ENV=1",
		"HERDR_PANE_ID=w1:p1",
		"HERDR_SOCKET_PATH=" + testjail.Place("/tmp/h.sock"),
		"HERDR_BIN_PATH=/usr/local/bin/herdr",
	}
	for _, test := range []struct {
		name, tmux, forced string
		args               []string
		stripped           bool
	}{
		{"print", "", "", []string{"-p", "hi"}, true},
		{"output format", "", "", []string{"--output-format=json", "hi"}, true},
		{"non-interactive subcommand", "", "", []string{"mcp", "list"}, true},
		{"help", "", "", []string{"--help"}, true},
		{"version", "", "", []string{"--version"}, true},
		{"forced pass-through", "", "1", []string{"--resume", "X"}, false},
		{"forced pass-through of a print run", "", "1", []string{"-p", "hi"}, false},
		{"inside a pfm socket", "/tmp/tmux-1000/cc-1-2-3,123,0", "", []string{"--resume", "X"}, false},
		{"print inside a pfm socket", "/tmp/tmux-1000/cc-1-2-3,123,0", "", []string{"-p", "hi"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			t.Setenv("TMUX", test.tmux)
			if test.tmux != "" {
				proveLauncherTmux(t)
			}
			t.Setenv("TMUX_PANE", "%1")
			t.Setenv("PFM_LAUNCH_PASSTHROUGH", test.forced)
			for _, entry := range herdrEnv {
				name, value, _ := strings.Cut(entry, "=")
				t.Setenv(name, value)
			}
			var got []string
			LaunchExec = func(_ string, _, environment []string) error {
				got = append([]string(nil), environment...)
				return nil
			}
			var stderr bytes.Buffer
			if code := Launch(
				append([]string{"--real", "/bin/echo", "--"}, test.args...),
				&bytes.Buffer{}, &stderr, config.Runtime{}, paths.OSEnv{},
			); code != 0 {
				t.Fatalf("Launch code=%d stderr=%q", code, stderr.String())
			}
			for _, entry := range herdrEnv {
				want := 1
				if test.stripped {
					want = 0
				}
				if count := countEnvironmentEntry(got, entry); count != want {
					t.Errorf("%q occurs %d times in the exec env, want %d", entry, count, want)
				}
			}
			if countEnvironmentEntry(got, "TMUX_PANE=%1") != 1 {
				t.Errorf("TMUX_PANE left the exec env: only the Herdr names are stripped here: %q", got)
			}
		})
	}
}

func proveLauncherTmux(t *testing.T) {
	t.Helper()
	tmux := testjail.TmuxRunner(deps.RunResult{Stdout: []byte("cc-fixture " + strconv.Itoa(os.Getpid()) + "\n")})
	previous := launchContext
	launchContext = func() context.Context { return pfmtmux.WithClientRunner(context.Background(), tmux) }
	t.Cleanup(func() { launchContext = previous })
}

// TestLaunchWithFindsTmuxThroughItsRunner pins the seam LaunchWith hands down:
// the tmux a chat launch needs is looked up through the runner the entry
// received, and a runner that cannot find it stops the launch with that error.
func TestLaunchWithFindsTmuxThroughItsRunner(t *testing.T) {
	testjail.Fleet(t)
	t.Setenv("TMUX", "")
	t.Setenv("PFM_LAUNCH_PASSTHROUGH", "")
	proveLauncherTmux(t)
	runner := &deps.FakeRunner{}
	var stderr bytes.Buffer
	code := LaunchWith(
		[]string{"--real", "/bin/echo", "--", "--resume", "fixture-session"},
		&bytes.Buffer{}, &stderr, config.Runtime{}, paths.OSEnv{}, runner,
	)
	want := "pfm internal launch: find tmux: exec: \"tmux\": executable file not found in $PATH\n"
	if code != 1 || stderr.String() != want {
		t.Fatalf("LaunchWith with a runner that finds no tmux = %d, %q; want 1, %q", code, stderr.String(), want)
	}
}
