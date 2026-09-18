package mockengine

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/testjail"
)

// helperEnv makes THIS test binary the engine: a symlink named claude, codex
// or opencode pointing at it re-enters TestMain, which dispatches to Run with
// the symlink's name as argv[0] — exactly how a real engine is found on PATH.
const helperEnv = "MOCK_ENGINE_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		os.Exit(Main(context.Background(), os.Args[0], os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
	}
	os.Exit(testjail.Run(m))
}

// fixture is one jailed host: a PATH dir holding the three engine names, a
// Claude config dir, a Codex home, an OpenCode data root, a fake /proc and a
// sid dir, plus the scenario file every mock process in it reads.
type fixture struct {
	t         *testing.T
	root      string
	bin       string
	home      string
	configDir string
	codexHome string
	procRoot  string
	sidDir    string
	recordDir string
	scenario  string
	work      string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	fix := &fixture{
		t:         t,
		root:      root,
		bin:       filepath.Join(root, "bin"),
		home:      filepath.Join(root, "home"),
		configDir: filepath.Join(root, "cc"),
		codexHome: filepath.Join(root, "codex"),
		procRoot:  filepath.Join(root, "proc"),
		sidDir:    filepath.Join(root, "sid"),
		recordDir: filepath.Join(root, "record"),
		scenario:  filepath.Join(root, "scenario.json"),
		work:      filepath.Join(root, "work", "repo"),
	}
	for _, directory := range []string{
		fix.bin, fix.home, fix.configDir, fix.codexHome, fix.procRoot, fix.sidDir, fix.recordDir, fix.work,
		filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid())),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "codex", "opencode"} {
		if err := os.Symlink(self, filepath.Join(fix.bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(helperEnv, "1")
	t.Setenv("PATH", fix.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", fix.home)
	t.Setenv("CLAUDE_CONFIG_DIR", fix.configDir)
	t.Setenv("CODEX_HOME", fix.codexHome)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TMUX_TMPDIR", root)
	t.Setenv(EnvScenario, fix.scenario)
	t.Setenv(EnvEngine, "")
	// pfm's own readers resolve their jail through internal/paths.
	t.Setenv(paths.EnvHome, fix.home)
	t.Setenv(paths.EnvDB, filepath.Join(root, "state", "fleet.db"))
	t.Setenv(paths.EnvFleetDB, filepath.Join(fix.home, ".cc", "fleet.db"))
	t.Setenv(paths.EnvSIDDir, fix.sidDir)
	t.Setenv(paths.EnvClaudeRoots, filepath.Join(fix.configDir, "projects"))
	t.Setenv(paths.EnvCodexHome, fix.codexHome)
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid())))
	t.Setenv(paths.EnvProcRoot, fix.procRoot)
	fix.write(Scenario{})
	return fix
}

// write installs a scenario, filling the jail bindings and the record dir so
// every test's mock lands its evidence in the fixture.
func (fix *fixture) write(scenario Scenario) {
	fix.t.Helper()
	if scenario.Jail == (Jail{}) {
		scenario.Jail = Jail{ProcRoot: fix.procRoot, SIDDir: fix.sidDir}
	}
	if scenario.RecordDir == "" {
		scenario.RecordDir = fix.recordDir
	}
	if err := scenario.Write(fix.scenario); err != nil {
		fix.t.Fatal(err)
	}
	if err := os.Remove(fix.scenario + cursorSuffix); err != nil && !os.IsNotExist(err) {
		fix.t.Fatal(err)
	}
}

// env is the process environment a mock run in-process reads.
func (fix *fixture) env(extra map[string]string) func(string) string {
	return func(name string) string {
		if value, ok := extra[name]; ok {
			return value
		}
		return os.Getenv(name)
	}
}

// screen is a stdout sink that keeps the frame after the last full repaint —
// what tmux capture-pane would show of a TUI that clears the screen each time.
type screen struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

var (
	clearScreen = "\x1b[2J\x1b[H"
	ansiEscape  = regexp.MustCompile(`\x1b\[[0-?]*[\x20-\x2f]*[\x40-\x7e]`)
)

func (out *screen) Write(content []byte) (int, error) {
	out.mu.Lock()
	defer out.mu.Unlock()
	return out.buf.Write(content)
}

// frame returns the current visible pane text, ANSI stripped.
func (out *screen) frame() string {
	out.mu.Lock()
	defer out.mu.Unlock()
	text := out.buf.String()
	if at := strings.LastIndex(text, clearScreen); at >= 0 {
		text = text[at+len(clearScreen):]
	}
	return ansiEscape.ReplaceAllString(text, "")
}

// all returns everything ever written, ANSI stripped.
func (out *screen) all() string {
	out.mu.Lock()
	defer out.mu.Unlock()
	return ansiEscape.ReplaceAllString(out.buf.String(), "")
}

// tui runs the engine in-process over a pipe-driven composer. Keys are typed
// through the returned writer; the frame is the pane.
type tui struct {
	t      *testing.T
	stdin  *os.File
	out    *screen
	stderr *bytes.Buffer
	done   chan int
	cancel context.CancelFunc
	exited bool
}

func (fix *fixture) startTUI(engine string, args []string, extraEnv map[string]string) *tui {
	fix.t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		fix.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	session := &tui{
		t: fix.t, stdin: writer, out: &screen{}, stderr: &bytes.Buffer{}, done: make(chan int, 1), cancel: cancel,
	}
	go func() {
		defer func() {
			if err := reader.Close(); err != nil {
				fix.t.Errorf("close tui stdin reader: %v", err)
			}
		}()
		session.done <- Main(ctx, engine, args, reader, session.out, session.stderr, fix.env(extraEnv))
	}()
	fix.t.Cleanup(func() {
		cancel()
		_ = writer.Close()
		if session.exited {
			return
		}
		select {
		case <-session.done:
		case <-time.After(5 * time.Second):
			fix.t.Errorf("mock %s did not exit after cancel; stderr=%q", engine, session.stderr.String())
		}
	})
	return session
}

func (session *tui) typeLine(text string) {
	session.t.Helper()
	if _, err := session.stdin.WriteString(text + "\r"); err != nil {
		session.t.Fatalf("type %q: %v", text, err)
	}
}

func (session *tui) typeRaw(text string) {
	session.t.Helper()
	if _, err := session.stdin.WriteString(text); err != nil {
		session.t.Fatalf("type raw %q: %v", text, err)
	}
}

// waitFrame polls the pane until want(frame) holds; it fails naming the last
// frame and stderr when it never does.
func (session *tui) waitFrame(what string, want func(string) bool) string {
	session.t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	frame := ""
	for time.Now().Before(deadline) {
		frame = session.out.frame()
		if want(frame) {
			return frame
		}
		time.Sleep(20 * time.Millisecond)
	}
	session.t.Fatalf("pane never showed %s; last frame:\n%s\nstderr=%q", what, frame, session.stderr.String())
	return frame
}

// waitExit waits for Run to return and gives back its exit code.
func (session *tui) waitExit() int {
	session.t.Helper()
	select {
	case code := <-session.done:
		session.exited = true
		return code
	case <-time.After(8 * time.Second):
		session.t.Fatalf("mock did not exit; frame:\n%s\nstderr=%q", session.out.frame(), session.stderr.String())
		return -1
	}
}

// waitFile polls until path exists and want(content) holds.
func waitFile(t *testing.T, path string, want func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(path)
		if err == nil {
			last = string(content)
			if want(last) {
				return last
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never held what was wanted; last content:\n%s", path, last)
	return last
}

func runOnce(fix *fixture, engine string, args []string, stdin string) (code int, stdout, stderr string) {
	fix.t.Helper()
	var out, errs bytes.Buffer
	code = Main(context.Background(), engine, args, strings.NewReader(stdin), &out, &errs, fix.env(nil))
	return code, out.String(), errs.String()
}

func TestMainRefusesAnArgv0ThatNamesNoEngine(t *testing.T) {
	fix := newFixture(t)
	code, stdout, stderr := runOnce(fix, "/opt/tools/2.1.238", []string{"--version"}, "")
	if code != ExitUsage {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want %d", code, stdout, stderr, ExitUsage)
	}
	if !strings.Contains(stderr, "2.1.238") || !strings.Contains(stderr, EnvEngine) {
		t.Fatalf("refusal must name the basename and the override: %q", stderr)
	}
}

func TestEngineEnvSelectsTheEngineForAVersionedPath(t *testing.T) {
	fix := newFixture(t)
	var out, errs bytes.Buffer
	code := Main(
		context.Background(), "/opt/tools/versions/2.1.238", []string{"--version"},
		strings.NewReader(""), &out, &errs, fix.env(map[string]string{EnvEngine: "claude"}),
	)
	if code != 0 || strings.TrimSpace(out.String()) != DefaultClaudeVersion {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, out.String(), errs.String())
	}
}

func TestVersionIsPinnedPerEngineAndOverridable(t *testing.T) {
	fix := newFixture(t)
	for engine, want := range map[string]string{
		"claude": DefaultClaudeVersion, "codex": DefaultCodexVersion, "opencode": DefaultOpenCodeVersion,
	} {
		code, stdout, stderr := runOnce(fix, engine, []string{"--version"}, "")
		if code != 0 || strings.TrimSpace(stdout) != want {
			t.Fatalf("%s --version exit=%d stdout=%q stderr=%q, want %q", engine, code, stdout, stderr, want)
		}
	}
	fix.write(Scenario{Version: "2.1.238 (Claude Code)"})
	code, stdout, _ := runOnce(fix, "claude", []string{"--version"}, "")
	if code != 0 || strings.TrimSpace(stdout) != "2.1.238 (Claude Code)" {
		t.Fatalf("scenario version exit=%d stdout=%q", code, stdout)
	}
}

func TestAMissingScenarioFileIsAnErrorNotTheDefault(t *testing.T) {
	fix := newFixture(t)
	if err := os.Remove(fix.scenario); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runOnce(fix, "claude", []string{"--version"}, "")
	if code != ExitUsage || !strings.Contains(stderr, fix.scenario) {
		t.Fatalf("exit=%d stderr=%q, want %d naming the scenario path", code, stderr, ExitUsage)
	}
}

func TestClaudeAgentsListsNoAgents(t *testing.T) {
	fix := newFixture(t)
	code, stdout, stderr := runOnce(fix, "claude", []string{"agents", "--json"}, "")
	if code != 0 || strings.TrimSpace(stdout) != "[]" {
		t.Fatalf("agents --json exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
