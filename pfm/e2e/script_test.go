//go:build e2e

package e2e

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"

	"hostops/pfm/internal/testjail"
)

const (
	e2eTmuxBinaryEnv   = "PFM_E2E_TMUX_BINARY"
	e2eScriptBinaryEnv = "PFM_E2E_SCRIPT_BINARY"
	e2eScriptRootEnv   = "PFM_E2E_SCRIPT_ROOT"
	e2eScriptOwnerEnv  = "PFM_E2E_SCRIPT_OWNER"
)

type jailedTestMain struct{ m *testing.M }

func (m jailedTestMain) Run() int {
	if os.Getenv("PFM_E2E_REQUIRE_FENCE_HELPER") == "1" ||
		os.Getenv("PFM_E2E_CLAUDE_CAPTURE") == "1" ||
		os.Getenv("PFM_E2E_CODEX_HOOK_FIXTURE") == "1" {
		return testjail.Run(m.m)
	}
	if os.Getenv("PFM_DEV_FENCE") != "1" {
		fmt.Fprintln(os.Stderr, "e2e harness refuses to run without PFM_DEV_FENCE=1")
		return 1
	}
	goBinary, err := exec.LookPath("go")
	if err != nil {
		fmt.Fprintf(os.Stderr, "TOOLCHAIN-MISSING go: %v\n", err)
		return 1
	}
	if strings.TrimSpace(os.Getenv(e2eTmuxBinaryEnv)) == "" {
		fmt.Fprintln(os.Stderr, "TOOLCHAIN-MISSING tmux: executable not found before testscript setup")
		return 1
	}
	if err := prepareScriptBinary(goBinary); err != nil {
		fmt.Fprintf(os.Stderr, "build e2e pfm binary: %v\n", err)
		return 1
	}
	code := testjail.Run(m.m)
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
		"jail-pfm":    runPFMCommand,
		"jail-tmux":   runTmuxCommand,
		"jail-until":  runSleepUntilCommand,
		"jail-pfm-rc": runPFMRCCommand,
	})
}

func prepareScriptBinary(goBinary string) error {
	if binary := strings.TrimSpace(os.Getenv(e2eScriptBinaryEnv)); binary != "" {
		if _, err := os.Stat(binary); err != nil {
			return fmt.Errorf("reuse %s=%s: %w", e2eScriptBinaryEnv, binary, err)
		}
		return prepareCoverageDirectory()
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("locate script harness source")
	}
	pfmRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), ".."))
	root, err := testjail.CreateShortRoot()
	if err != nil {
		return fmt.Errorf("create short binary root: %w", err)
	}
	binary := filepath.Join(root, "pfm")
	command := exec.Command(
		goBinary, "build", "-cover", "-covermode=atomic", "-ldflags", "-X main.version=test", "-o", binary, "./cmd/pfm",
	)
	command.Dir = pfmRoot
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOTELEMETRY=off")
	if output, buildErr := command.CombinedOutput(); buildErr != nil {
		_ = os.RemoveAll(root)
		return fmt.Errorf("%w: %s", buildErr, strings.TrimSpace(string(output)))
	}
	for name, value := range map[string]string{
		e2eScriptBinaryEnv: binary,
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

func prepareCoverageDirectory() error {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("locate coverage root")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	coverRoot := strings.TrimSpace(os.Getenv("COVER_DIR"))
	if coverRoot == "" {
		coverRoot = filepath.Join(repositoryRoot, "tmp", "cover")
	} else if !filepath.IsAbs(coverRoot) {
		coverRoot = filepath.Join(repositoryRoot, coverRoot)
	}
	coverDir := filepath.Join(coverRoot, "e2e")
	if err := os.MkdirAll(coverDir, 0o700); err != nil {
		return fmt.Errorf("create coverage directory: %w", err)
	}
	if err := os.Setenv("GOCOVERDIR", coverDir); err != nil {
		return fmt.Errorf("set GOCOVERDIR: %w", err)
	}
	return nil
}

func TestScripts(t *testing.T) {
	requireE2EFence(t)
	source := sourceRepo(t)
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
	claudeRoot := filepath.Join(root, "claude")
	codexRoot := filepath.Join(root, "codex")
	binDir := filepath.Join(root, "bin")
	for _, directory := range []string{
		filepath.Join(home, ".claude"), filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".config", "pfm"), filepath.Join(home, ".cc"),
		claudeRoot, codexRoot, filepath.Join(root, "sid"), filepath.Join(root, "proc"),
		tmuxDir, filepath.Join(root, "state"), filepath.Join(root, "tmp"), binDir,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create jail directory %s: %w", directory, err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".claude-primary"), []byte("2\n"), 0o600); err != nil {
		return fmt.Errorf("write primary account fixture: %w", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(scriptClaudeFixture), 0o700); err != nil {
		return fmt.Errorf("write Claude fixture: %w", err)
	}
	nativeClaude := filepath.Join(home, ".local", "share", "claude", "versions", "2.1.238")
	if err := os.MkdirAll(filepath.Dir(nativeClaude), 0o700); err != nil {
		return fmt.Errorf("create native Claude fixture directory: %w", err)
	}
	if err := os.WriteFile(nativeClaude, []byte(scriptClaudeFixture), 0o700); err != nil {
		return fmt.Errorf("write native Claude fixture: %w", err)
	}
	if err := os.Symlink(nativeClaude, filepath.Join(home, ".local", "bin", "claude")); err != nil {
		return fmt.Errorf("link native Claude fixture: %w", err)
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
		"HOME": home, "PFM_HOME": home,
		"PFM_DEV_FENCE":    "1",
		e2eScriptBinaryEnv: os.Getenv(e2eScriptBinaryEnv),
		e2eTmuxBinaryEnv:   os.Getenv(e2eTmuxBinaryEnv),
		"PFM_DB":           filepath.Join(root, "state", "fleet.db"),
		"PFM_FLEET_DB":     filepath.Join(home, ".cc", "fleet.db"),
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
		"PFM_SOURCE_REPO":       scriptSource,
		"PFM_HARVESTPY_OFFLINE": "1",
		"CC_STUB_TRANSCRIPT":    filepath.Join(claudeRoot, "fixture", "b1111111-1111-4111-8111-111111111111.jsonl"),
		"PATH":                  path,
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

const scriptClaudeFixture = `#!/usr/bin/env bash
if [ "${1-}" = agents ]; then printf '[]\n'; exit 0; fi
if [ "${1-}" = --version ]; then printf '2.1.238 (Claude Code)\n'; exit 0; fi
stty -icanon -echo -ixon min 1 time 0 2>/dev/null
sock="${TMUX%%,*}"; sock="${sock##*/}"
transcript="$CC_STUB_TRANSCRIPT"
mkdir -p "$(dirname "$transcript")" "$PFM_SID_DIR" "$PFM_PROC_ROOT/$$/fd"
: > "$transcript"
printf '%s\n' "$transcript" > "$PFM_SID_DIR/$sock"
printf 'claude\0--jailed\0' > "$PFM_PROC_ROOT/$$/cmdline"
printf '%s (claude) S %s 1 1 0 -1 0 0 0 0 0 0 0 0 0 0 0 20 0 100\n' "$$" "$PPID" > "$PFM_PROC_ROOT/$$/stat"
: > "$PFM_PROC_ROOT/$$/environ"
trap 'rm -rf "$PFM_PROC_ROOT/$$"' EXIT
esc() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'; }
turn() {
  printf '{"type":"user","sessionId":"b1111111-1111-4111-8111-111111111111","cwd":"%s","message":{"content":"%s"}}\n' "$(esc "$PWD")" "$(esc "$1")" >> "$transcript"
  printf '{"type":"assistant","message":{"content":[{"type":"text","text":"ack: %s"}]}}\n' "$(esc "$1")" >> "$transcript"
  printf '\r\nUSER:%s\r\nack: %s\r\n' "$1" "$1"
}
prompt=""; name=""; take=""
for argument in "$@"; do
  if [ -n "$take" ]; then
    [ "$take" = name ] && name="$argument"
    take=""
    continue
  fi
  case "$argument" in
    --name) take=name ;;
    --model|--effort) take=skip ;;
    -*) ;;
    *) prompt="$argument"; break ;;
  esac
done
[ -n "$name" ] && printf '{"type":"custom-title","customTitle":"%s"}\n' "$(esc "$name")" >> "$transcript"
printf 'claude ready\r\n❯ '
[ -n "$prompt" ] && turn "$prompt"
buf=""
while IFS= read -r -N1 ch; do
  case "$ch" in
    $'\n'|$'\r') [ -n "$buf" ] && turn "$buf"; buf=""; printf '❯ ' ;;
    $'\033') ;;
    $'\177'|$'\b') buf="${buf%?}" ;;
    *) buf="$buf$ch"; printf '%s' "$ch" ;;
  esac
done
`
