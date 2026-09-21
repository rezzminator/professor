package gather

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

type tmuxJail struct {
	root      string
	tmuxDir   string
	sidDir    string
	codexHome string
	home      string
	sockets   []string
}

type alwaysFailTmux struct{}

func (alwaysFailTmux) ListPanes(context.Context, string) ([]ProbePane, error) {
	return nil, fmt.Errorf("dead server")
}

type failOnceTmux struct {
	calls int
}

func (tmux *failOnceTmux) ListPanes(
	context.Context,
	string,
) ([]ProbePane, error) {
	tmux.calls++
	if tmux.calls == 1 {
		return nil, fmt.Errorf("transient server race")
	}
	return []ProbePane{{Socket: "cc-1-2-3", PaneID: "%1"}}, nil
}

func newTmuxJail(t *testing.T) *tmuxJail {
	t.Helper()

	root, err := os.MkdirTemp("/tmp", "ccf")
	if err != nil {
		t.Fatalf("create short tmux jail: %v", err)
	}
	jail := &tmuxJail{
		root:      root,
		tmuxDir:   filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid())),
		sidDir:    filepath.Join(root, "sid"),
		codexHome: filepath.Join(root, "codex"),
		home:      filepath.Join(root, "home"),
	}
	setGatherTestEnv(t, jail.root, jail.tmuxDir)

	t.Cleanup(func() {
		for _, socket := range jail.sockets {
			command := jail.command("-L", socket, "kill-server")
			_ = command.Run()
		}
		if err := os.RemoveAll(jail.root); err != nil {
			t.Errorf("remove tmux jail %q: %v", jail.root, err)
		}
	})
	return jail
}

func setGatherTestEnv(t *testing.T, root, tmuxDir string) {
	t.Helper()

	home := filepath.Join(root, "home")
	sidDir := filepath.Join(root, "sid")
	codexHome := filepath.Join(root, "codex")
	for _, directory := range []string{home, sidDir, codexHome, tmuxDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create gather jail directory %q: %v", directory, err)
		}
	}

	t.Setenv("HOME", home)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", root)
	t.Setenv(paths.EnvHome, home)
	t.Setenv(paths.EnvDB, filepath.Join(root, "fleet.db"))
	t.Setenv(paths.EnvSIDDir, sidDir)
	t.Setenv(paths.EnvClaudeRoots, filepath.Join(root, "claude-projects"))
	t.Setenv(paths.EnvCodexHome, codexHome)
	t.Setenv(paths.EnvTmuxDir, tmuxDir)
	// A chat server loads the user's ~/.tmux.conf in real life; a fixture must
	// not, or the machine it runs on steers the test.
	t.Setenv(paths.EnvTmuxConf, "/dev/null")
}

func (jail *tmuxJail) command(arguments ...string) *exec.Cmd {
	command := exec.Command("tmux", arguments...)
	command.Env = append(
		os.Environ(),
		"HOME="+jail.home,
		"TMUX=",
		"TMUX_TMPDIR="+jail.root,
	)
	return command
}

func (jail *tmuxJail) startServer(
	t *testing.T,
	socket, session, window, title string,
) {
	t.Helper()

	command := jail.command(
		"-f",
		"/dev/null",
		"-L",
		socket,
		"new-session",
		"-d",
		"-s",
		session,
		"-n",
		window,
		"sleep 120",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start jailed tmux server %q: %v: %s", socket, err, output)
	}
	jail.sockets = append(jail.sockets, socket)

	command = jail.command(
		"-L",
		socket,
		"select-pane",
		"-t",
		session+":0.0",
		"-T",
		title,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("set jailed pane title for %q: %v: %s", socket, err, output)
	}
}

func (jail *tmuxJail) killServer(socket string) error {
	return jail.command("-L", socket, "kill-server").Run()
}

func createCorpseSocket(t *testing.T, path string, modified time.Time) {
	t.Helper()

	listener, err := net.ListenUnix("unix", &net.UnixAddr{
		Name: path,
		Net:  "unix",
	})
	if err != nil {
		t.Fatalf("create corpse socket %q: %v", path, err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatalf("close corpse socket %q: %v", path, err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatalf("set corpse socket mtime %q: %v", path, err)
	}
}

func TestProbeTmuxReadOnlyLeavesOldCorpse(t *testing.T) {
	tmuxDir := t.TempDir()
	path := filepath.Join(tmuxDir, "cc-7-8-9")
	now := time.Now()
	createCorpseSocket(t, path, now.Add(-2*time.Hour))
	probe, err := ProbeTmuxReadOnly(
		context.Background(),
		tmuxDir,
		alwaysFailTmux{},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(probe.CorpseSwept) != 0 || len(probe.ProbeWarnings) != 1 {
		t.Fatalf("read-only probe = %#v", probe)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("read-only probe removed old corpse: %v", err)
	}
}

// TestDeadSocketIsSweptWithoutAWarning: a socket whose server has exited is a
// chat that ended, and every picker close used to shout one
// "tmux probe warning: <sock>: exit status 1" per leftover. The corpse is still
// swept — silently — while a genuine probe failure still speaks up.
func TestDeadSocketIsSweptWithoutAWarning(t *testing.T) {
	tmuxDir := t.TempDir()
	path := filepath.Join(tmuxDir, "cc-7-8-9")
	now := time.Now()
	createCorpseSocket(t, path, now.Add(-2*time.Hour))

	probe, err := ProbeTmux(context.Background(), tmuxDir, goneServerTmux{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(probe.ProbeWarnings) != 0 {
		t.Fatalf("a dead socket still warned: %#v", probe.ProbeWarnings)
	}
	if len(probe.CorpseSwept) != 1 || probe.CorpseSwept[0] != "cc-7-8-9" {
		t.Fatalf("dead socket was not swept: %#v", probe.CorpseSwept)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corpse socket survived the sweep: %v", err)
	}

	// The contrast: anything that is NOT "no server" is still an anomaly.
	other := t.TempDir()
	createCorpseSocket(t, filepath.Join(other, "cc-7-8-9"), now.Add(-2*time.Hour))
	loud, err := ProbeTmux(context.Background(), other, alwaysFailTmux{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(loud.ProbeWarnings) != 1 {
		t.Fatalf("a real probe failure went silent: %#v", loud)
	}
	// ...and an anomaly is NEVER swept. An unreadable probe says nothing about
	// whether a server is there, so deleting the socket does not clean up after
	// a dead chat — it detaches a live one from every client and lookup that
	// reaches it by path, leaving a healthy chat looking merely resumable.
	if len(loud.CorpseSwept) != 0 {
		t.Fatalf("a socket the probe could not READ was swept: %#v", loud.CorpseSwept)
	}
	if _, err := os.Stat(filepath.Join(other, "cc-7-8-9")); err != nil {
		t.Fatalf("an unreadable probe deleted the socket: %v", err)
	}
}

// TestServerGoneReadsTmuxOwnWords runs the real tmux against a socket with
// nothing behind it: the exit status alone says nothing (every failure is 1),
// so the classifier has to read the stderr exec keeps on the ExitError.
func TestServerGoneReadsTmuxOwnWords(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	command := exec.Command("tmux", "-L", "cc-no-such-server-9999", "list-panes", "-a")
	command.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+dir)
	_, err := command.Output()
	if err == nil {
		t.Fatal("probing an absent tmux server unexpectedly succeeded")
	}
	if err.Error() != "exit status 1" {
		t.Logf("note: tmux error text is %q", err.Error())
	}
	if !serverGone(err) {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("tmux said %q and serverGone missed it", string(exit.Stderr))
		}
		t.Fatalf("serverGone missed %v", err)
	}
}

type goneServerTmux struct{}

func (goneServerTmux) ListPanes(_ context.Context, socket string) ([]ProbePane, error) {
	return nil, fmt.Errorf("%w: %s", ErrServerGone, socket)
}

func TestProbeTmuxRetriesOneTransientReadFailure(t *testing.T) {
	tmuxDir := t.TempDir()
	path := filepath.Join(tmuxDir, "cc-1-2-3")
	createCorpseSocket(t, path, time.Now())
	client := &failOnceTmux{}
	probe, err := ProbeTmuxReadOnly(
		context.Background(),
		tmuxDir,
		client,
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 2 ||
		len(probe.Panes) != 1 ||
		len(probe.ProbeWarnings) != 0 {
		t.Fatalf("retry probe=%#v calls=%d", probe, client.calls)
	}
}

func TestParseLegacyPaneFallback(t *testing.T) {
	output := "session\tpane title\t/work/project\t4\t1\t100\t%7\t/dev/pts/9\tClaude\t321\n"
	panes, err := parseLegacyPaneOutput("cc-1-2-3", []byte(output))
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 1 ||
		panes[0].SessionName != "session" ||
		panes[0].PaneID != "%7" ||
		panes[0].WindowName != "Claude" ||
		panes[0].WindowID != "" ||
		panes[0].PID != 321 ||
		!panes[0].Attached {
		t.Fatalf("legacy fallback panes = %#v", panes)
	}
}

func TestJailedTmuxProbeAndGather(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary is not installed")
	}

	jail := newTmuxJail(t)
	const (
		ccSocket = "cc-101-202-303"
		cxSocket = "cx-404-505-606"
	)
	jail.startServer(t, ccSocket, "cc-session", "claude-old", "claude-pane")
	jail.startServer(t, cxSocket, "cx-session", "codex-old", "codex-pane")
	jail.startServer(t, "vsct-test", "vsct-session", "ignored", "ignored")
	jail.startServer(t, "misc-test", "misc-session", "ignored", "ignored")

	now := time.Now()
	oldCorpse := "cc-707-808-909"
	freshCorpse := "cx-111-222-333"
	oldCorpsePath := filepath.Join(jail.tmuxDir, oldCorpse)
	freshCorpsePath := filepath.Join(jail.tmuxDir, freshCorpse)
	createCorpseSocket(t, oldCorpsePath, now.Add(-2*time.Hour))
	createCorpseSocket(t, freshCorpsePath, now.Add(-30*time.Minute))

	client := TmuxProbe{Binary: "tmux", TmuxTmpDir: jail.root}
	probe, err := ProbeTmux(context.Background(), jail.tmuxDir, client, now)
	if err != nil {
		t.Fatalf("ProbeTmux() error = %v", err)
	}
	if got := paneSockets(probe.Panes); !reflect.DeepEqual(
		got,
		[]string{ccSocket, cxSocket},
	) {
		entries, _ := os.ReadDir(jail.tmuxDir)
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			info, _ := entry.Info()
			names = append(names, fmt.Sprintf("%s:%v", entry.Name(), info.Mode()))
		}
		t.Fatalf(
			"ProbeTmux() sockets = %q, want only chat sockets; warnings=%q entries=%q",
			got,
			probe.ProbeWarnings,
			names,
		)
	}
	if !reflect.DeepEqual(probe.CorpseSwept, []string{oldCorpse}) {
		t.Fatalf("ProbeTmux().CorpseSwept = %q, want old corpse", probe.CorpseSwept)
	}
	// Both corpses are swept in SILENCE: a socket with no server behind it is
	// a chat that ended, which is the most ordinary thing in a fleet. It used
	// to raise one warning apiece, so every picker close ended in a wall of
	// "exit status 1" that buried the failures worth reading.
	if len(probe.ProbeWarnings) != 0 {
		t.Fatalf("ProbeTmux().ProbeWarnings = %q, want silence for corpses", probe.ProbeWarnings)
	}
	if _, err := os.Stat(oldCorpsePath); !os.IsNotExist(err) {
		t.Fatalf("old corpse still exists: %v", err)
	}
	if _, err := os.Stat(freshCorpsePath); err != nil {
		t.Fatalf("fresh corpse was removed: %v", err)
	}

	paneBySocket := make(map[string]ProbePane)
	for _, pane := range probe.Panes {
		paneBySocket[pane.Socket] = pane
	}
	ccPane := paneBySocket[ccSocket]
	cxPane := paneBySocket[cxSocket]
	if ccPane.SessionName != "cc-session" ||
		ccPane.WindowName != "claude-old" ||
		ccPane.PaneTitle != "claude-pane" ||
		ccPane.CurrentPath == "" ||
		ccPane.TTY == "" ||
		ccPane.PID <= 0 ||
		ccPane.PaneID == "" {
		t.Fatalf("ProbeTmux() cc pane = %+v, want exact jailed fields", ccPane)
	}
	if cxPane.SessionName != "cx-session" ||
		cxPane.WindowName != "codex-old" ||
		cxPane.PaneTitle != "codex-pane" ||
		cxPane.TTY == "" ||
		cxPane.PID <= 0 ||
		cxPane.PaneID == "" {
		t.Fatalf("ProbeTmux() cx pane = %+v, want exact jailed fields", cxPane)
	}
	for name, content := range map[string]string{
		ccSocket:                       "/transcripts/socket.jsonl",
		cxSocket + "." + cxPane.PaneID: "/transcripts/pane.jsonl",
		ccSocket + ".%999":             "/transcripts/stale.jsonl",
		ccSocket + ".then-failed":      "/transcripts/sentinel.jsonl",
	} {
		if err := os.WriteFile(filepath.Join(jail.sidDir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write jailed crumb %q: %v", name, err)
		}
	}

	const (
		codexPID = 900001
		agentPID = 900002
		session  = "01234567-89ab-cdef-0123-456789abcdef"
	)
	rolloutPath := filepath.Join(jail.codexHome, "sessions", "2026", "rollout-live.jsonl")
	writeRolloutMeta(t, rolloutPath, "user", "")
	writeRolloutMeta(t, filepath.Join(jail.codexHome, "sessions", "rollout-later.jsonl"), "subagent", "live")
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		cxPane.PID: {},
		ccPane.PID: {},
		codexPID: {
			cmdline: []string{"/usr/bin/codex"},
			fdLinks: []FDLink{
				{FD: 9, Target: filepath.Join(jail.codexHome, "sessions", "rollout-later.jsonl")},
				{FD: 3, Target: rolloutPath},
			},
			stat: ProcStat{ParentPID: cxPane.PID, StartTime: 10},
		},
		agentPID: {
			cmdline: []string{"/usr/bin/claude", "--session-id", session},
			environ: map[string]string{
				"CLAUDE_CONFIG_DIR":        filepath.Join(jail.home, ".cc", "2"),
				"ENABLE_PROMPT_CACHING_1H": "1",
			},
			stat: ProcStat{ParentPID: ccPane.PID, StartTime: 20},
		},
	}}
	longName := strings.Repeat("界", 25)
	gatherer, err := New(Dependencies{
		ProcFS:    proc,
		Tmux:      client,
		Now:       func() time.Time { return now },
		CodexName: func(string) string { return longName },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	snapshot, err := gatherer.Gather(context.Background())
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	if got := paneSockets(snapshot.Panes); !reflect.DeepEqual(
		got,
		[]string{ccSocket, cxSocket},
	) {
		t.Fatalf("Gather().Panes sockets = %q", got)
	}
	if len(snapshot.Crumbs) != 2 {
		t.Fatalf("Gather().Crumbs = %#v, want socket and pane crumbs", snapshot.Crumbs)
	}
	if len(snapshot.Codex) != 1 ||
		snapshot.Codex[0].Socket != cxSocket ||
		snapshot.Codex[0].RolloutPath != rolloutPath {
		t.Fatalf("Gather().Codex = %#v", snapshot.Codex)
	}
	if len(snapshot.Agents) != 1 ||
		snapshot.Agents[0].Socket != ccSocket ||
		snapshot.Agents[0].SessionID != session {
		t.Fatalf("Gather().Agents = %#v", snapshot.Agents)
	}
	if len(snapshot.ClaudeProcesses) != 1 ||
		snapshot.ClaudeProcesses[0].PID != agentPID ||
		snapshot.ClaudeProcesses[0].Socket != ccSocket {
		t.Fatalf("Gather().ClaudeProcesses = %#v", snapshot.ClaudeProcesses)
	}
	if !reflect.DeepEqual(snapshot.Cache1HSockets, []string{ccSocket}) {
		t.Fatalf("Gather().Cache1HSockets = %q", snapshot.Cache1HSockets)
	}
	if len(snapshot.Renames) != 1 ||
		snapshot.Renames[0].TargetName != strings.Repeat("界", 24) {
		t.Fatalf("Gather().Renames = %#v", snapshot.Renames)
	}
	if !reflect.DeepEqual(snapshot.StaleSwept, []string{ccSocket + ".%999"}) {
		t.Fatalf("Gather().StaleSwept = %q", snapshot.StaleSwept)
	}
	if _, err := os.Stat(filepath.Join(jail.sidDir, ccSocket+".then-failed")); err != nil {
		t.Fatalf("sentinel was touched by Gather(): %v", err)
	}
}

// TestJailedResumedCodexPaneNamingAndConflicts fixtures the 2026-08-11
// mislabel on real tmux servers. A pane resumed into an elder thread holds no
// rollout file and no rollout descriptor, so the rename used to key on nothing
// and the window kept a sibling's name. Its argv states the thread outright.
// The same pass also proves the two guards around that rung: two windows that
// would take ONE name take neither, and a pane holding a rollout descriptor
// still converges through the path it always did.
func TestJailedResumedCodexPaneNamingAndConflicts(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary is not installed")
	}

	jail := newTmuxJail(t)
	const (
		resumedSocket = "cx-901-1-1"
		firstTwin     = "cx-902-2-2"
		secondTwin    = "cx-903-3-3"
		freshSocket   = "cx-904-4-4"
		// the thread the owner resumed through the picker, named in
		// session_index alone: AWCX, then AWD — the last write is the name.
		resumedThread = "019feb1b-1215-7f30-b18b-e227e5ca26e5"
		firstTwinID   = "019fed7f-aac7-7650-8a8b-96ab595179ab"
		secondTwinID  = "019fedaa-1111-7650-8a8b-96ab59517abc"
	)
	for _, socket := range []string{
		resumedSocket,
		firstTwin,
		secondTwin,
		freshSocket,
	} {
		jail.startServer(t, socket, socket, "BUILDER_WF", "codex-pane")
	}

	now := time.Now()
	client := TmuxProbe{Binary: "tmux", TmuxTmpDir: jail.root}
	probe, err := ProbeTmux(context.Background(), jail.tmuxDir, client, now)
	if err != nil {
		t.Fatalf("ProbeTmux() error = %v", err)
	}
	paneBySocket := make(map[string]ProbePane, len(probe.Panes))
	for _, pane := range probe.Panes {
		paneBySocket[pane.Socket] = pane
	}

	rolloutPath := filepath.Join(
		jail.codexHome,
		"sessions",
		"2026",
		"rollout-fresh.jsonl",
	)
	writeRolloutMeta(t, rolloutPath, "user", "")
	processes := map[int]fakeProcess{}
	for _, socket := range []string{
		resumedSocket,
		firstTwin,
		secondTwin,
		freshSocket,
	} {
		pane, found := paneBySocket[socket]
		if !found {
			t.Fatalf("ProbeTmux() lost jailed socket %q", socket)
		}
		processes[pane.PID] = fakeProcess{}
	}
	resumedPID := paneBySocket[resumedSocket].PID + 100000
	processes[resumedPID] = fakeProcess{
		cmdline: []string{
			"/opt/codex",
			"--dangerously-bypass-approvals-and-sandbox",
			"resume",
			resumedThread,
		},
		stat: ProcStat{ParentPID: paneBySocket[resumedSocket].PID},
	}
	for _, twin := range []struct {
		socket string
		thread string
	}{
		{socket: firstTwin, thread: firstTwinID},
		{socket: secondTwin, thread: secondTwinID},
	} {
		processes[paneBySocket[twin.socket].PID+100000] = fakeProcess{
			cmdline: []string{"/opt/codex", "resume", twin.thread},
			stat:    ProcStat{ParentPID: paneBySocket[twin.socket].PID},
		}
	}
	processes[paneBySocket[freshSocket].PID+100000] = fakeProcess{
		cmdline: []string{"/opt/codex"},
		fdLinks: []FDLink{{FD: 3, Target: rolloutPath}},
		stat:    ProcStat{ParentPID: paneBySocket[freshSocket].PID},
	}

	namesByID := map[string]string{
		resumedThread: "AWD",
		firstTwinID:   "TWIN",
		secondTwinID:  "TWIN",
	}
	gatherer, err := New(Dependencies{
		ProcFS:      &fakeProcFS{processes: processes},
		Tmux:        client,
		Now:         func() time.Time { return now },
		CodexName:   func(path string) string { return map[string]string{rolloutPath: "FRESH_THREAD"}[path] },
		CodexIDName: func(threadID string) string { return namesByID[threadID] },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	snapshot, err := gatherer.Gather(context.Background())
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	codexBySocket := make(map[string]LiveCodex, len(snapshot.Codex))
	for _, live := range snapshot.Codex {
		codexBySocket[live.Socket] = live
	}
	if resumed := codexBySocket[resumedSocket]; resumed.ThreadID != resumedThread ||
		resumed.RolloutPath != "" {
		t.Fatalf(
			"resumed pane = %#v, want the argv thread id and no rollout file",
			resumed,
		)
	}

	got := make([][2]string, 0, len(snapshot.Renames))
	for _, rename := range snapshot.Renames {
		got = append(got, [2]string{rename.Socket, rename.TargetName})
	}
	want := [][2]string{
		{resumedSocket, "AWD"},
		{freshSocket, "FRESH_THREAD"},
	}
	sort.Slice(want, func(left, right int) bool {
		return want[left][0] < want[right][0]
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf(
			"Gather().Renames = %v, want the resumed and fresh panes only "+
				"(the twins share one name and must take neither)",
			got,
		)
	}
}

func paneSockets(panes []ProbePane) []string {
	sockets := make([]string, 0, len(panes))
	for index := range panes {
		pane := panes[index]
		sockets = append(sockets, pane.Socket)
	}
	return sockets
}

func TestTmuxJailUsesShortPrivateSocketPaths(t *testing.T) {
	jail := newTmuxJail(t)
	socketPath := filepath.Join(jail.tmuxDir, "cc-1-2-3")
	if len(socketPath) >= 100 {
		t.Fatalf("jailed socket path is too long (%d): %q", len(socketPath), socketPath)
	}
	if !strings.HasPrefix(filepath.Clean(jail.root), "/tmp/ccf") {
		t.Fatalf("tmux jail escaped short private root: %q", jail.root)
	}
}
