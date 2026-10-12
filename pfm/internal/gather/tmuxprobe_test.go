package gather

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

func TestShowGlobalOptionAndIdentityNudgeClassifyGoneServer(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\necho 'no server running on fake socket' >&2\nexit 1\n"
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	const socket = "cc-stale-1-2-3"
	client := TmuxProbe{Binary: binary, TmuxTmpDir: t.TempDir()}

	_, err := client.ShowGlobalOption(context.Background(), socket, "set-titles-string")
	if !errors.Is(err, ErrServerGone) {
		t.Fatalf("ShowGlobalOption error = %v, want ErrServerGone", err)
	}
	if !strings.Contains(err.Error(), socket) {
		t.Fatalf("ShowGlobalOption error = %q, want socket context", err)
	}
	err = client.NudgeTitlesIdentity(context.Background(), socket)
	if !errors.Is(err, ErrServerGone) {
		t.Fatalf("NudgeTitlesIdentity error = %v, want ErrServerGone", err)
	}
	if !strings.Contains(err.Error(), socket) {
		t.Fatalf("NudgeTitlesIdentity error = %q, want socket context", err)
	}
}

func TestNudgeTitlesStringRoundTripsExplicitIdentity(t *testing.T) {
	client, state, commands := newTitlesProbeFixture(t)
	const socket = "cc-explicit-identity"

	if err := client.NudgeTitlesString(context.Background(), socket, "#P"); err != nil {
		t.Fatal(err)
	}

	gotState, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotState) != "#P\n" {
		t.Fatalf("identity after nudge = %q, want #P", strings.TrimSuffix(string(gotState), "\n"))
	}
	gotCommands, err := os.ReadFile(commands)
	if err != nil {
		t.Fatal(err)
	}
	wantCommands := "-L " + socket + " set-option -g set-titles-string #P \n" +
		"-L " + socket + " set-option -g set-titles-string #P\n"
	if string(gotCommands) != wantCommands {
		t.Fatalf("tmux commands = %q, want %q", gotCommands, wantCommands)
	}
}

func TestNudgeTitlesIdentityReadsAndRestoresSocketValue(t *testing.T) {
	client, state, commands := newTitlesProbeFixture(t)
	const socket = "cc-socket-identity"

	if err := client.NudgeTitlesIdentity(context.Background(), socket); err != nil {
		t.Fatal(err)
	}

	gotState, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotState) != "#T\n" {
		t.Fatalf("identity after nudge = %q, want #T", strings.TrimSuffix(string(gotState), "\n"))
	}
	gotCommands, err := os.ReadFile(commands)
	if err != nil {
		t.Fatal(err)
	}
	wantCommands := "-L " + socket + " show -gv set-titles-string\n" +
		"-L " + socket + " set-option -g set-titles-string #T \n" +
		"-L " + socket + " set-option -g set-titles-string #T\n"
	if string(gotCommands) != wantCommands {
		t.Fatalf("tmux commands = %q, want %q", gotCommands, wantCommands)
	}
}

func newTitlesProbeFixture(t *testing.T) (TmuxProbe, string, string) {
	t.Helper()
	root := t.TempDir()
	binary := filepath.Join(root, "tmux")
	state := filepath.Join(root, "state")
	commands := filepath.Join(root, "commands")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$PFM_TEST_TMUX_COMMANDS"
case "$3" in
show)
	cat "$PFM_TEST_TMUX_STATE"
	;;
set-option)
	printf '%s\n' "$6" > "$PFM_TEST_TMUX_STATE"
	;;
*)
	exit 2
	;;
esac
`
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("#T\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_TEST_TMUX_COMMANDS", commands)
	t.Setenv("PFM_TEST_TMUX_STATE", state)
	return TmuxProbe{Binary: binary, TmuxTmpDir: root}, state, commands
}

// TestProbeTmuxFailsWholeWhenTmuxCannotRun is the regression for the shared
// MCP daemon going deaf: launchd started it with /usr/bin:/bin:/usr/sbin:/sbin,
// tmux lives outside that PATH, and every socket's list-panes failed to START.
// Each failure was filed as a per-socket warning, so the probe returned zero
// panes and no error — every chat_inject from a Codex chat then read "matched
// no live chat" when the truth was "could not look". A tmux that never ran
// read no socket at all, so the pass fails whole and names the cause.
//
// The binary is a bare name absent from PATH, exactly what deps.Executable
// hands back when its lookup fails, so this is the live error shape.
func TestProbeTmuxFailsWholeWhenTmuxCannotRun(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, probe := range []struct {
		name string
		run  func(context.Context, string, TmuxClient, time.Time) (TmuxSnapshot, error)
	}{
		{name: "sweeping", run: ProbeTmux},
		{name: "read-only", run: ProbeTmuxReadOnly},
	} {
		t.Run(probe.name, func(t *testing.T) {
			tmuxDir := t.TempDir()
			now := time.Now()
			createCorpseSocket(t, filepath.Join(tmuxDir, "cc-7-8-9"), now.Add(-2*time.Hour))
			client := TmuxProbe{Binary: "pfm-test-missing-tmux", TmuxTmpDir: tmuxDir}

			result, err := probe.run(context.Background(), tmuxDir, client, now)
			if err == nil {
				t.Fatalf(
					"probe with an unstartable tmux returned no error: panes=%d warnings=%q — an empty fleet that means \"could not look\"",
					len(result.Panes),
					result.ProbeWarnings,
				)
			}
			if !pfmtmux.CouldNotRun(err) {
				t.Fatalf("probe error %v does not carry the could-not-run cause", err)
			}
			if !strings.Contains(err.Error(), "pfm-test-missing-tmux") {
				t.Fatalf("probe error %q does not name the binary it could not run", err)
			}
			// A probe that could not read a socket must never sweep it.
			if _, statErr := os.Stat(filepath.Join(tmuxDir, "cc-7-8-9")); statErr != nil {
				t.Fatalf("probe that could not run removed a socket it never read: %v", statErr)
			}
		})
	}
}

// TestProbeTmuxKeepsAFailingServerAsAWarning pins the other side of the
// line: a tmux that RAN and failed against one server is that server's
// problem, reported as a warning while the rest of the fleet still lists.
func TestProbeTmuxKeepsAFailingServerAsAWarning(t *testing.T) {
	tmuxDir := t.TempDir()
	now := time.Now()
	createCorpseSocket(t, filepath.Join(tmuxDir, "cc-7-8-9"), now)
	result, err := ProbeTmuxReadOnly(context.Background(), tmuxDir, alwaysFailTmux{}, now)
	if err != nil {
		t.Fatalf("one failing server failed the whole probe: %v", err)
	}
	if len(result.ProbeWarnings) != 1 {
		t.Fatalf("warnings = %q, want the one failing server named", result.ProbeWarnings)
	}
}

// ConvergeGlobalOptions applies only what diverges and names each change as
// it read it, so a second pass over a converged server changes nothing and
// says nothing; a server that cannot be read is an error naming the option,
// never an empty "nothing to converge".
func TestConvergeGlobalOptionsChangesOnlyWhatDiverges(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root, err := os.MkdirTemp("/tmp", "pfmcv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socket := "cc-1800000041-1-1"
	environment := append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
	start := exec.Command("tmux", "-L", socket, "-f", "/dev/null", "new-session", "-d", "-s", socket, "sleep", "120")
	start.Env = environment
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start tmux fixture: %v: %s", err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-L", socket, "kill-server")
		kill.Env = environment
		_ = kill.Run()
	})
	client := TmuxProbe{Binary: "tmux", TmuxTmpDir: root}
	options := [][]string{
		{"set-option", "-g", "set-titles", "off"},
		{"set-window-option", "-g", "automatic-rename", "off"},
	}
	transitions, err := client.ConvergeGlobalOptions(context.Background(), socket, options)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(transitions, "; ") != `automatic-rename "on" -> "off"` {
		t.Fatalf("first pass transitions = %q, want only automatic-rename changed", transitions)
	}
	if again, err := client.ConvergeGlobalOptions(
		context.Background(),
		socket,
		options,
	); err != nil ||
		len(again) != 0 {
		t.Fatalf("second pass = %q (%v), want a converged server left alone", again, err)
	}
	_, err = client.ConvergeGlobalOptions(context.Background(), "cc-missing", options)
	if err == nil || !strings.Contains(err.Error(), "set-titles") {
		t.Fatalf("unreadable server error = %v, want one naming the option it could not read", err)
	}
}

// TestEmptyServerIsListedWithTheReapFix runs real tmux: a pfm chat server left
// running with no session answers list-panes -a with "no current target" and
// exit 1, which the probe used to pass on as a bare "exit status 1" warning.
// It is now ErrServerEmpty, listed in EmptyServers with a warning naming the
// socket and the reap command, and its socket is never swept.
func TestEmptyServerIsListedWithTheReapFix(t *testing.T) {
	jail := newTmuxJail(t)
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "1")
	const socket = "probe-empty-server"
	jail.startServer(t, socket, "doomed", "work", "title")
	for _, arguments := range [][]string{
		{"set-option", "-g", "exit-empty", "off"},
		{"kill-session", "-t", "doomed"},
	} {
		if output, err := jail.command(append([]string{"-L", socket}, arguments...)...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %v on %s: %v: %s", arguments, socket, err, output)
		}
	}
	ctx := context.Background()
	client := TmuxProbe{TmuxTmpDir: jail.root}
	if _, err := client.ListPanes(ctx, socket); !errors.Is(err, ErrServerEmpty) {
		t.Fatalf("an empty server probed as %v, want ErrServerEmpty", err)
	}
	if pid, err := client.ServerPID(ctx, socket); err != nil || pid <= 0 {
		t.Fatalf("ServerPID on the empty server = %d, %v", pid, err)
	}
	probe, err := ProbeTmux(ctx, jail.tmuxDir, client, time.Now().Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(probe.EmptyServers, []string{socket}) ||
		!reflect.DeepEqual(probe.ProbeWarnings, []string{EmptyServerWarning(socket)}) {
		t.Fatalf("probe = %#v, want the empty server listed once", probe)
	}
	if want := socket + ": abandoned empty tmux server"; !strings.HasPrefix(probe.ProbeWarnings[0], want) ||
		!strings.HasSuffix(probe.ProbeWarnings[0], "reap it: pfm chat ls --reap") {
		t.Fatalf("warning = %q, want it to name the server and the reap command", probe.ProbeWarnings[0])
	}
	if len(probe.CorpseSwept) != 0 {
		t.Fatalf("the socket of a running empty server was swept: %v", probe.CorpseSwept)
	}
}

// TestProbeFailureCarriesTmuxReasonAndAHandCheck: a probe that failed for a
// reason other than an empty or dead server stays a failure — never
// ErrServerEmpty, never absence — and its warning carries tmux's own stderr
// and the command that checks the server by hand.
func TestProbeFailureCarriesTmuxReasonAndAHandCheck(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\necho 'tmux: connect failed: permission denied' >&2\nexit 1\n"
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	const socket = "cc-7-8-9"
	tmuxDir := t.TempDir()
	createCorpseSocket(t, filepath.Join(tmuxDir, socket), time.Now())
	client := TmuxProbe{Binary: binary, TmuxTmpDir: t.TempDir()}
	_, err := client.ListPanes(context.Background(), socket)
	if err == nil || errors.Is(err, ErrServerEmpty) || errors.Is(err, ErrServerGone) ||
		!strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("ListPanes error = %v, want a failure carrying tmux's reason", err)
	}
	probe, err := ProbeTmux(context.Background(), tmuxDir, client, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := socket + ": could not read this tmux server: list-panes: exit status 1: " +
		"tmux: connect failed: permission denied — check it by hand: tmux -L " + socket + " list-sessions"
	if !reflect.DeepEqual(probe.ProbeWarnings, []string{want}) || len(probe.EmptyServers) != 0 {
		t.Fatalf("probe = %#v, want the one failure warning %q", probe, want)
	}
}
