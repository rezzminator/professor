//go:build e2e

package e2e

import (
	"bytes"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/mockengine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

const (
	e2eTmuxBinaryEnv   = "PFM_E2E_TMUX_BINARY"
	e2eScriptBinaryEnv = "PFM_E2E_SCRIPT_BINARY"
	e2eScriptRootEnv   = "PFM_E2E_SCRIPT_ROOT"
	e2eScriptOwnerEnv  = "PFM_E2E_SCRIPT_OWNER"
	// e2eMockEngineEnv is the built cmd/mock-engine binary every script jail
	// installs as its `claude` (internal/mockengine).
	e2eMockEngineEnv = "PFM_E2E_MOCK_ENGINE"
)

var e2eCoverageAtStart = os.Getenv("GOCOVERDIR")

type jailedTestMain struct{ m *testing.M }

func (m jailedTestMain) Run() int {
	if os.Getenv("PFM_E2E_REQUIRE_FENCE_HELPER") == "1" ||
		os.Getenv("PFM_E2E_CLAUDE_CAPTURE") == "1" ||
		os.Getenv("PFM_E2E_CLAUDE_PLUGIN") == "1" ||
		os.Getenv("PFM_E2E_CODEX_HOOK_FIXTURE") == "1" {
		return testjail.Run(m.m)
	}
	if os.Getenv("PFM_DEV_FENCE") != "1" {
		fmt.Fprintln(os.Stderr, "e2e harness refuses to run without PFM_DEV_FENCE=1")
		return 1
	}
	if _, err := exec.LookPath("go"); err != nil {
		fmt.Fprintf(os.Stderr, "TOOLCHAIN-MISSING go: %v\n", err)
		return 1
	}
	if strings.TrimSpace(os.Getenv(e2eTmuxBinaryEnv)) == "" {
		fmt.Fprintln(os.Stderr, "TOOLCHAIN-MISSING tmux: executable not found before testscript setup")
		return 1
	}
	if err := prepareScriptBinary(); err != nil {
		fmt.Fprintf(os.Stderr, "build e2e pfm binary: %v\n", err)
		return 1
	}
	var err error
	e2eStageRoot, err = os.MkdirTemp("", "pfm-e2e-stage-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create e2e stage root: %v\n", err)
		return 1
	}
	code := testjail.Run(m.m)
	if err := os.RemoveAll(e2eStageRoot); err != nil {
		fmt.Fprintf(os.Stderr, "remove e2e stage root: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(e2eScriptOwnerEnv) == strconv.Itoa(os.Getpid()) {
		if root := os.Getenv(e2eScriptRootEnv); root != "" {
			if err := os.RemoveAll(root); err != nil && code == 0 {
				fmt.Fprintf(os.Stderr, "remove built binary root: %v\n", err)
				code = 1
			}
		}
	}
	return code
}

func TestMain(m *testing.M) {
	tmuxBinary := strings.TrimSpace(os.Getenv(e2eTmuxBinaryEnv))
	if tmuxBinary == "" {
		if resolved, err := exec.LookPath("tmux"); err == nil {
			tmuxBinary = resolved
		}
		if err := os.Setenv(e2eTmuxBinaryEnv, tmuxBinary); err != nil {
			fmt.Fprintf(os.Stderr, "set %s: %v\n", e2eTmuxBinaryEnv, err)
			os.Exit(1)
		}
	}
	testscript.Main(jailedTestMain{m}, map[string]func(){
		"jail-pfm":      runPFMCommand,
		"jail-tmux":     runTmuxCommand,
		"jail-until":    runSleepUntilCommand,
		"jail-ls-lacks": runLSLacksCommand,
		"jail-pfm-rc":   runPFMRCCommand,
	})
}

func prepareScriptBinary() error {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("locate script harness source")
	}
	pfmRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), ".."))
	if binary := strings.TrimSpace(os.Getenv(e2eScriptBinaryEnv)); binary != "" {
		if _, err := os.Stat(binary); err != nil {
			return fmt.Errorf("reuse %s=%s: %w", e2eScriptBinaryEnv, binary, err)
		}
		if strings.TrimSpace(os.Getenv(e2eMockEngineEnv)) == "" {
			root, err := testjail.CreateShortRoot()
			if err != nil {
				return fmt.Errorf("create short mock-engine root: %w", err)
			}
			if err := buildMockEngine(pfmRoot, root); err != nil {
				_ = os.RemoveAll(root)
				return err
			}
			for name, value := range map[string]string{
				e2eScriptRootEnv:  root,
				e2eScriptOwnerEnv: strconv.Itoa(os.Getpid()),
			} {
				if err := os.Setenv(name, value); err != nil {
					_ = os.RemoveAll(root)
					return fmt.Errorf("set %s: %w", name, err)
				}
			}
		}
		return prepareCoverageDirectory()
	}
	root, err := testjail.CreateShortRoot()
	if err != nil {
		return fmt.Errorf("create short binary root: %w", err)
	}
	binary := filepath.Join(root, "pfm")
	flags := []string{"-ldflags", "-X main.version=test"}
	// To feed make cover, run the e2e suite with GOCOVERDIR=$COVER_DIR/e2e.
	if os.Getenv("GOCOVERDIR") != "" {
		flags = append(flags, "-cover", "-covermode=atomic")
	}
	mockEngine := filepath.Join(root, "mock-engine")
	if err := buildConcurrently(
		func() error { return testjail.GoBuild(pfmRoot, binary, "./cmd/pfm", flags...) },
		func() error { return compileMockEngine(pfmRoot, mockEngine) },
	); err != nil {
		_ = os.RemoveAll(root)
		return err
	}
	for name, value := range map[string]string{
		e2eScriptBinaryEnv: binary,
		e2eMockEngineEnv:   mockEngine,
		e2eScriptRootEnv:   root,
		e2eScriptOwnerEnv:  strconv.Itoa(os.Getpid()),
	} {
		if err := os.Setenv(name, value); err != nil {
			_ = os.RemoveAll(root)
			return fmt.Errorf("set %s: %w", name, err)
		}
	}
	return prepareCoverageDirectory()
}

// buildConcurrently runs every build at once and waits for all of them. Each
// failure keeps its own error text, which names its target; together they are
// joined in argument order.
func buildConcurrently(builds ...func() error) error {
	errs := make([]error, len(builds))
	var group sync.WaitGroup
	for index, build := range builds {
		group.Go(func() { errs[index] = build() })
	}
	group.Wait()
	return errors.Join(errs...)
}

// compileMockEngine builds cmd/mock-engine to binary.
func compileMockEngine(pfmRoot, binary string) error {
	if err := testjail.GoBuild(pfmRoot, binary, "./cmd/mock-engine"); err != nil {
		return fmt.Errorf("build mock-engine: %w", err)
	}
	return nil
}

// buildMockEngine builds cmd/mock-engine into root and publishes its path.
func buildMockEngine(pfmRoot, root string) error {
	binary := filepath.Join(root, "mock-engine")
	if err := compileMockEngine(pfmRoot, binary); err != nil {
		return err
	}
	if err := os.Setenv(e2eMockEngineEnv, binary); err != nil {
		return fmt.Errorf("set %s: %w", e2eMockEngineEnv, err)
	}
	return nil
}

func prepareCoverageDirectory() error {
	coverDir := os.Getenv("GOCOVERDIR")
	if coverDir == "" {
		return nil
	}
	if err := os.MkdirAll(coverDir, 0o700); err != nil {
		return fmt.Errorf("create coverage directory: %w", err)
	}
	return nil
}

func TestScriptHarnessBuildSettings(t *testing.T) {
	t.Parallel()
	requireE2EFence(t)
	if got := os.Getenv("GOCOVERDIR"); got != e2eCoverageAtStart {
		t.Errorf("GOCOVERDIR after harness = %q, want original %q", got, e2eCoverageAtStart)
	}
	binary := os.Getenv(e2eScriptBinaryEnv)
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatalf("read harness pfm build info: %v", err)
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	for key, want := range map[string]string{
		"-trimpath":   "true",
		"CGO_ENABLED": "0",
	} {
		if got := settings[key]; got != want {
			t.Errorf("harness pfm build setting %s = %q, want %q", key, got, want)
		}
	}
	output, err := exec.Command(binary, "version").CombinedOutput()
	if err != nil || string(output) != "pfm test\n" {
		t.Errorf("harness pfm version: output=%q err=%v, want pfm test", output, err)
	}
}

func TestScriptBinaryBuildsRunConcurrently(t *testing.T) {
	t.Parallel()
	requireE2EFence(t)
	started := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	build := func(own, other int) func() error {
		return func() error {
			close(started[own])
			select {
			case <-started[other]:
				return nil
			case <-time.After(10 * time.Second):
				return fmt.Errorf("build %d waited 10s for build %d to start", own, other)
			}
		}
	}
	if err := buildConcurrently(build(0, 1), build(1, 0)); err != nil {
		t.Fatalf("builds did not overlap: %v", err)
	}
}

func TestScriptBinaryBuildFailureNamesItsTarget(t *testing.T) {
	t.Parallel()
	requireE2EFence(t)
	empty := t.TempDir()
	pfmBuild := func() error { return testjail.GoBuild(empty, filepath.Join(empty, "pfm"), "./cmd/pfm") }
	mockBuild := func() error { return compileMockEngine(empty, filepath.Join(empty, "mock-engine")) }
	succeeds := func() error { return nil }

	err := buildConcurrently(pfmBuild, succeeds)
	if err == nil || !strings.HasPrefix(err.Error(), "go build ./cmd/pfm: ") {
		t.Fatalf("pfm build failure = %v, want it to name ./cmd/pfm", err)
	}
	err = buildConcurrently(succeeds, mockBuild)
	if err == nil || !strings.HasPrefix(err.Error(), "build mock-engine: go build ./cmd/mock-engine: ") {
		t.Fatalf("mock-engine build failure = %v, want it to name ./cmd/mock-engine", err)
	}
	err = buildConcurrently(pfmBuild, mockBuild)
	if err == nil {
		t.Fatal("both builds failed, want an error")
	}
	pfmAt := strings.Index(err.Error(), "go build ./cmd/pfm: ")
	mockAt := strings.Index(err.Error(), "build mock-engine: go build ./cmd/mock-engine: ")
	if pfmAt < 0 || mockAt < pfmAt {
		t.Fatalf("both builds failed = %v, want the pfm failure then the mock-engine failure", err)
	}
}

// TestScripts stays serial: install-init.txtar runs `pfm install` with no
// PFM_MANAGED_SETTINGS_DIR, so it writes the machine managed-settings drop-in
// (/etc/claude-code/managed-settings.d/pfm.json) that
// TestInstallInitUpdateUninstallE2E reads back.
func TestScripts(t *testing.T) {
	requireE2EFence(t)
	source := sharedSourceRepo(t)
	testscript.Run(t, testscript.Params{
		Dir:                 "testdata/scripts",
		RequireExplicitExec: true,
		RequireUniqueNames:  true,
		Setup: func(env *testscript.Env) error {
			return setupScriptJail(env, source)
		},
	})
}

func setupScriptJail(env *testscript.Env, source string) error {
	root, err := testjail.CreateShortRoot()
	if err != nil {
		return fmt.Errorf("create short script jail: %w", err)
	}
	env.Defer(func() {
		cleanupTmux(root)
		if err := os.RemoveAll(root); err != nil && !errors.Is(err, fs.ErrNotExist) {
			env.T().Log(fmt.Sprintf("remove script jail %s: %v", root, err))
		}
	})
	home := filepath.Join(root, "home")
	tmuxBase := filepath.Join(root, "t")
	tmuxDir := filepath.Join(tmuxBase, "tmux-"+strconv.Itoa(os.Getuid()))
	// Account 1 writes beneath its own config dir. Its projects link points
	// at HOME/.claude/projects so the mock engine transcript reaches the index.
	claudeRoot := filepath.Join(home, ".claude", "projects")
	codexRoot := filepath.Join(root, "codex")
	binDir := filepath.Join(root, "bin")
	for _, directory := range []string{
		claudeRoot, filepath.Join(home, ".local", "bin"),
		pfmconfig.DefaultAccountDir(home, 1),
		codexRoot, filepath.Join(root, "sid"), filepath.Join(root, "proc"),
		tmuxDir, filepath.Join(root, "state"), filepath.Join(root, "tmp"), binDir,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create jail directory %s: %w", directory, err)
		}
	}
	if err := os.Symlink(claudeRoot, filepath.Join(pfmconfig.DefaultAccountDir(home, 1), "projects")); err != nil {
		return fmt.Errorf("link account 1 projects to the indexed root: %w", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude-primary"), []byte("2\n"), 0o600); err != nil {
		return fmt.Errorf("write primary account fixture: %w", err)
	}
	// The mock engine is the jail's Claude: on PATH under the engine's own
	// name (argv[0] selects the engine) and at the native versions path the
	// launcher execs by absolute path (MOCK_ENGINE_ENGINE names the engine
	// there, since that basename is a version number).
	mockEngine := strings.TrimSpace(os.Getenv(e2eMockEngineEnv))
	if mockEngine == "" {
		return fmt.Errorf("%s is unset: the mock engine was not built before the script jail", e2eMockEngineEnv)
	}
	if err := os.Symlink(mockEngine, filepath.Join(binDir, "claude")); err != nil {
		return fmt.Errorf("link Claude mock on PATH: %w", err)
	}
	nativeClaude := filepath.Join(home, ".local", "share", "claude", "versions", "2.1.238")
	if err := os.MkdirAll(filepath.Dir(nativeClaude), 0o700); err != nil {
		return fmt.Errorf("create native Claude fixture directory: %w", err)
	}
	if err := os.Symlink(mockEngine, nativeClaude); err != nil {
		return fmt.Errorf("link native Claude fixture: %w", err)
	}
	if err := os.Symlink(nativeClaude, filepath.Join(home, ".local", "bin", "claude")); err != nil {
		return fmt.Errorf("link native Claude fixture: %w", err)
	}
	busy := 0
	scenario := filepath.Join(root, "mock-engine.json")
	if err := (mockengine.Scenario{
		Version:   "2.1.238 (Claude Code)",
		SessionID: "b1111111-1111-4111-8111-111111111111",
		BusyMS:    &busy,
		Jail:      mockengine.Jail{ProcRoot: filepath.Join(root, "proc"), SIDDir: filepath.Join(root, "sid")},
	}).Write(scenario); err != nil {
		return fmt.Errorf("write mock-engine scenario: %w", err)
	}
	if err := writeSchedulerFixtures(home); err != nil {
		return err
	}
	scriptSource := source
	if strings.HasSuffix(env.WorkDir, "script-install-init") {
		scriptSource = filepath.Join(env.WorkDir, "source")
		command := exec.Command("git", "clone", "--no-local", "--quiet", source, scriptSource)
		command.Env = append(
			os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "HOME="+home,
		)
		if output, cloneErr := command.CombinedOutput(); cloneErr != nil {
			return fmt.Errorf(
				"stage private install source under WORK: %w: %s",
				cloneErr,
				strings.TrimSpace(string(output)),
			)
		}
		if err := os.Symlink(scriptSource, filepath.Join(home, ".professor")); err != nil {
			return fmt.Errorf("link jailed Professor source: %w", err)
		}
	}
	path := binDir + string(os.PathListSeparator) + filepath.Join(home, ".local", "bin") +
		string(os.PathListSeparator) + env.Getenv("PATH")
	values := map[string]string{
		"HOME": home, "PFM_HOME": home, "PFM_CONFIG": filepath.Join(home, "pfm.config.json"),
		"PFM_DEV_FENCE":    "1",
		e2eScriptBinaryEnv: os.Getenv(e2eScriptBinaryEnv),
		e2eTmuxBinaryEnv:   os.Getenv(e2eTmuxBinaryEnv),
		"PFM_CACHE_DB":     filepath.Join(root, "state", "pfm-cache.db"),
		"PFM_STATE_DB":     filepath.Join(home, ".local", "state", "pfm", "pfm.db"),
		"PFM_SID_DIR":      filepath.Join(root, "sid"),
		"PFM_CLAUDE_ROOTS": claudeRoot,
		"PFM_CODEX_ROOT":   codexRoot,
		"PFM_TMUX_DIR":     tmuxDir,
		"PFM_PROC_ROOT":    filepath.Join(root, "proc"),
		"PFM_TMUX_CONF":    "/dev/null",
		"TMUX_TMPDIR":      tmuxBase, "TMPDIR": filepath.Join(root, "tmp"),
		"XDG_CONFIG_HOME":   filepath.Join(home, ".config"),
		"CLAUDE_CONFIG_DIR": filepath.Join(root, "cc"),
		"TMUX":              "", "TMUX_PANE": "", "CLAUDE_CODE_SESSION_ID": "", "CODEX_THREAD_ID": "",
		"CHAT_SENDER_SESSION": "", "CHAT_SENDER_LABEL": "", "CHAT_SENDER_SID": "",
		"PFM_TEST_FRESH_SOCKET":  "cc-1700000000-4242-7",
		"PFM_TEST_NOW_NS":        "1800000000000000000",
		"PFM_TEST_PROBE_SOCKETS": "1",
		"PFM_E2E_HOME":           home, e2eSourceRepo: scriptSource,
		"PFM_SOURCE_REPO":           scriptSource,
		"PFM_HARVESTPY_OFFLINE":     "1",
		"PFM_SKILL_SOURCES_OFFLINE": "1",
		"PFM_THEMES_OFFLINE":        "1",
		mockengine.EnvScenario:      scenario,
		mockengine.EnvEngine:        "claude",
		"PATH":                      path,
	}
	for name, value := range values {
		env.Setenv(name, value)
	}
	return nil
}

func cleanupTmux(root string) {
	tmuxDir := filepath.Join(root, "t", "tmux-"+strconv.Itoa(os.Getuid()))
	entries, err := os.ReadDir(tmuxDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		command := exec.Command(os.Getenv(e2eTmuxBinaryEnv), "-S", filepath.Join(tmuxDir, entry.Name()), "kill-server")
		command.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+filepath.Join(root, "t"))
		_ = command.Run()
	}
}

func runPFMCommand() { replaceProcess(os.Getenv(e2eScriptBinaryEnv), os.Args[1:]) }

func runTmuxCommand() {
	if len(os.Args) < 3 || os.Args[1] != "-L" || os.Args[2] == "" || filepath.Base(os.Args[2]) != os.Args[2] {
		fmt.Fprintln(os.Stderr, "usage: jail-tmux -L <scratch-socket> <args>...")
		os.Exit(2)
	}
	replaceProcess(os.Getenv(e2eTmuxBinaryEnv), os.Args[1:])
}

func replaceProcess(binary string, args []string) {
	if binary == "" {
		fmt.Fprintln(os.Stderr, "TOOLCHAIN-MISSING registered subprocess binary")
		os.Exit(127)
	}
	if err := syscall.Exec(binary, append([]string{filepath.Base(binary)}, args...), os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "exec %s: %v\n", binary, err)
		os.Exit(127)
	}
}

func runPFMRCCommand() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: pfm-rc <exit-code> <args>...")
		os.Exit(2)
	}
	want, err := strconv.Atoi(os.Args[1])
	if err != nil || want < 0 || want > 255 {
		fmt.Fprintf(os.Stderr, "pfm-rc: invalid exit code %q\n", os.Args[1])
		os.Exit(2)
	}
	command := exec.Command(os.Getenv(e2eScriptBinaryEnv), os.Args[2:]...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = command.Run()
	got := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			fmt.Fprintf(os.Stderr, "pfm-rc: run pfm: %v\n", err)
			os.Exit(1)
		}
		got = exitErr.ExitCode()
	}
	if got != want {
		fmt.Fprintf(os.Stderr, "pfm-rc: got exit %d, want %d\n", got, want)
		os.Exit(1)
	}
}

func runSleepUntilCommand() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: sleep-until <socket> <target> <pattern>")
		os.Exit(2)
	}
	deadline := time.Now().Add(10 * time.Second)
	var last []byte
	var lastErr error
	for time.Now().Before(deadline) {
		command := exec.Command(
			os.Getenv(e2eTmuxBinaryEnv), "-L", os.Args[1], "capture-pane", "-p", "-J", "-S", "-", "-t", os.Args[2],
		)
		last, lastErr = command.CombinedOutput()
		if lastErr == nil && strings.Contains(string(last), os.Args[3]) {
			_, _ = os.Stdout.Write(last)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "sleep-until: timeout pattern=%q error=%v capture=%q\n", os.Args[3], lastErr, last)
	os.Exit(1)
}

func runLSLacksCommand() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: ls-lacks <name>")
		os.Exit(2)
	}
	deadline := time.Now().Add(10 * time.Second)
	var last []byte
	var lastErr error
	for time.Now().Before(deadline) {
		command := exec.Command(os.Getenv(e2eScriptBinaryEnv), "chat", "ls", "--all")
		last, lastErr = command.Output()
		if lastErr == nil && !strings.Contains(string(last), os.Args[1]) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "ls-lacks: timeout name=%q error=%v stdout=%q\n", os.Args[1], lastErr, last)
	os.Exit(1)
}

func TestLSLacksUsage(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"worker", "extra"}} {
		command := exec.Command(os.Args[0], args...)
		command.Args[0] = "jail-ls-lacks"
		var stderr bytes.Buffer
		command.Stderr = &stderr
		err := command.Run()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 || stderr.String() != "usage: ls-lacks <name>\n" {
			t.Fatalf("ls-lacks %q: error=%v stderr=%q, want exit 2 and usage", args, err, stderr.String())
		}
	}
}
