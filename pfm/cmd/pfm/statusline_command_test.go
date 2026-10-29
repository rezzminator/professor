package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/statusline"
)

func statuslineTestRuntime(t *testing.T) commandRuntime {
	t.Helper()
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func TestStatuslineCommandRendersFromJailedInput(t *testing.T) {
	jailTest(t)
	root := t.TempDir()
	cacheDir := filepath.Join(root, "tmp")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_HOME", root)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, ".cc", "1"))
	t.Setenv("PFM_TMUX_DIR", filepath.Join(root, "tmux"))
	t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))

	var stdout, stderr bytes.Buffer
	code := runStatuslineWithRuntime(
		nil,
		strings.NewReader(`{"model":{"display_name":"Opus 4"}}`),
		&stdout,
		&stderr,
		statuslineTestRuntime(t),
		paths.OSEnv{},
	)
	if code != 0 || !strings.Contains(stdout.String(), "◆ Opus 4") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestStatuslineOnABrokenConfigRendersTheConfigWarning(t *testing.T) {
	root := jailTest(t)
	configPath := writeConfigFixture(t, root, "{")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// The cache segment needs the session_id, so the chat is paced; a worker
	// that cannot start makes the front render inline, through the runtime
	// the worker renders with, and this tick shows the warning itself.
	original := statuslinePacedSpawn
	t.Cleanup(func() { statuslinePacedSpawn = original })
	statuslinePacedSpawn = func(string) error { return errors.New("no worker in this test") }
	if _, err := writer.WriteString(`{"session_id":"broken-config","model":{"display_name":"Opus 4"}}`); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	previous := os.Stdin
	os.Stdin = reader
	defer func() {
		os.Stdin = previous
		_ = reader.Close()
	}()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", configPath, "statusline"}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "Opus 4") || !strings.Contains(stdout.String(), "💾⚠config") {
		t.Fatalf("broken config statusline code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestUsageHookOnABrokenConfigStopsAtTheDispatcher(t *testing.T) {
	root := jailTest(t)
	configPath := writeConfigFixture(t, root, "{")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", configPath, "usage-hook"}, &stdout, &stderr); code != 1 ||
		!strings.HasPrefix(stderr.String(), "pfm: config:") {
		t.Fatalf("broken config usage-hook code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestDetachedRefreshCLIPathWritesCodexCacheInTwinHome(t *testing.T) {
	jailTest(t)
	root := t.TempDir()
	cacheDir := filepath.Join(root, "tmp")
	t.Setenv("PFM_HOME", root)

	originalCodex := statuslineCodexOptions
	t.Cleanup(func() {
		statuslineCodexOptions = originalCodex
	})
	statuslineCodexOptions = func() statusline.CodexOptions {
		return statusline.CodexOptions{
			Now: func() time.Time { return time.Unix(1_786_838_400, 0) },
			ReadRateLimits: func(context.Context) ([]byte, error) {
				return os.ReadFile(
					filepath.Join("..", "..", "internal", "statusline", "testdata", "gpt-app-server.jsonl"),
				)
			},
		}
	}

	var stdout, stderr bytes.Buffer
	if code := runStatuslineWithRuntime([]string{"--refresh-gpt"}, strings.NewReader(""), &stdout, &stderr,
		statuslineTestRuntime(t), paths.OSEnv{}); code != 0 {
		t.Fatalf("--refresh-gpt code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	path := filepath.Join(cacheDir, "cc-gpt-usage-"+strconv.Itoa(os.Getuid())+".json")
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		t.Fatalf("native refresher did not write %s: info=%v err=%v", path, info, err)
	}
}

func TestInternalStatuslineRoutesToNativeCommandContract(t *testing.T) {
	jailTest(t)
	var directStdout, directStderr bytes.Buffer
	directCode := runStatuslineWithRuntime(
		[]string{"unexpected"},
		strings.NewReader("not read for a usage error"),
		&directStdout,
		&directStderr,
		commandRuntime{},
		nil,
	)

	var internalStdout, internalStderr bytes.Buffer
	internalCode := runInternal(
		[]string{"statusline", "unexpected"},
		&internalStdout,
		&internalStderr,
		commandRuntime{},
	)

	if internalCode != directCode || internalStdout.String() != directStdout.String() ||
		internalStderr.String() != directStderr.String() {
		t.Fatalf(
			"internal statusline = code %d stdout %q stderr %q, direct = code %d stdout %q stderr %q",
			internalCode,
			internalStdout.String(),
			internalStderr.String(),
			directCode,
			directStdout.String(),
			directStderr.String(),
		)
	}
}

func TestStatuslineAndUsageHookCommandsFailOpen(t *testing.T) {
	jailTest(t)
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runStatuslineWithRuntime(nil, strings.NewReader("{"), &stdout, &stderr,
		statuslineTestRuntime(t), paths.OSEnv{}); code != 0 ||
		!strings.Contains(stderr.String(), "fail-open") {
		t.Fatalf("malformed statusline: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	configDir := filepath.Join(root, ".claude")
	target := filepath.Join(root, "squatted-cache")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"fixture"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	cacheLink := filepath.Join(root, "tmp", "cc-usage-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(filepath.Dir(cacheLink), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, cacheLink); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_HOME", root)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	stdout.Reset()
	stderr.Reset()
	if code := runUsageHookWithRuntime(
		nil,
		&stdout,
		&stderr,
		statuslineTestRuntime(t),
		paths.OSEnv{},
	); code != 0 ||
		stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "fail-open") {
		t.Fatalf("usage hook: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestCodexSeatUsageHookNeverTouchesClaudeCredentials(t *testing.T) {
	jailTest(t)
	root := t.TempDir()
	claudeConfig := filepath.Join(root, "claude-must-not-be-read")
	if err := os.WriteFile(claudeConfig, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", filepath.Join(root, ".codex-2"))
	t.Setenv("CODEX_THREAD_ID", "thread-2")
	// The seat is Codex-only: an inherited Claude session id from the shell
	// this suite runs in would make the hook take the Claude path.
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfig)
	var stdout, stderr bytes.Buffer
	if code := runUsageHookWithRuntime(
		nil,
		&stdout,
		&stderr,
		commandRuntime{},
		nil,
	); code != 0 || stdout.Len() != 0 ||
		stderr.Len() != 0 {
		t.Fatalf(
			"Codex usage hook touched Claude state: code=%d stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
}

// A sub-agent launched without an effort runs at its session's effort; the
// main statusline records that effort per session and the agent-panel row of
// the same session shows it when Claude Code's row payload carries none.
func TestStatuslineRecordsTheSessionEffortForTheAgentPanel(t *testing.T) {
	jailTest(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_HOME", root)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, ".cc", "1"))
	t.Setenv("PFM_TMUX_DIR", filepath.Join(root, "tmux"))
	t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))

	var stdout, stderr bytes.Buffer
	runtime := statuslineTestRuntime(t)
	if code := runStatuslineWithRuntime(
		nil,
		strings.NewReader(`{"session_id":"sess-eff","model":{"id":"claude-opus-5-5[1m]",`+
			`"display_name":"Opus"},"effort":{"level":"xhigh"},"context_window":{"used_percentage":5}}`),
		&stdout,
		&stderr,
		runtime,
		paths.OSEnv{},
	); code != 0 ||
		stderr.Len() != 0 {
		t.Fatalf("main line: code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	if code := runStatuslineWithRuntime(
		[]string{"--subagents"},
		strings.NewReader(`{"session_id":"sess-eff","tasks":[{"id":"t1",`+
			`"model":"claude-opus-5-5","contextWindowSize":1000,"tokenCount":10}]}`),
		&stdout,
		&stderr,
		runtime,
		paths.OSEnv{},
	); code != 0 ||
		stderr.Len() != 0 {
		t.Fatalf("row: code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "xhigh") {
		t.Fatalf("row = %q, want the session's effort xhigh", stdout.String())
	}
}

func TestStatuslineSubagentsAnswersTheAgentPanel(t *testing.T) {
	jailTest(t)
	root := t.TempDir()
	t.Setenv("PFM_HOME", root)
	t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))

	var stdout, stderr bytes.Buffer
	runtime := statuslineTestRuntime(t)
	code := runStatuslineWithRuntime(
		[]string{"--subagents"},
		strings.NewReader(`{"columns":100,"tasks":[{"id":"t1","label":"trace it","model":"claude-opus-5-5",`+
			`"contextWindowSize":1000000,"tokenCount":500000}]}`),
		&stdout,
		&stderr,
		runtime,
		paths.OSEnv{},
	)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var row struct{ ID, Content string }
	if err := json.Unmarshal(stdout.Bytes(), &row); err != nil || row.ID != "t1" ||
		!strings.Contains(row.Content, "50%") || !strings.Contains(row.Content, "trace it") {
		t.Fatalf("stdout=%q (err %v), want one {id:t1} row carrying 50%% and the label", stdout.String(), err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runStatuslineWithRuntime([]string{"--subagents"}, strings.NewReader(`{"tasks":`), &stdout, &stderr,
		runtime, paths.OSEnv{}); code != 0 ||
		stdout.Len() != 0 || !strings.Contains(stderr.String(), "--subagents: render (fail-open)") {
		t.Fatalf("malformed payload: code=%d stdout=%q stderr=%q, want exit 0, no rows, named error", code,
			stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runStatuslineWithRuntime([]string{"--subagents", "--refresh-gpt"}, strings.NewReader(""), &stdout,
		&stderr, runtime, paths.OSEnv{}); code != 2 {
		t.Fatalf("--subagents with --refresh-gpt: code=%d, want usage error 2", code)
	}
}

// The main statusline is a paced front: the first tick of a chat starts the
// chat's worker and prints the pending line, rendering nothing and caching
// nothing; a later tick prints the worker's cached line byte for byte, renders
// nothing, and starts no second worker while one holds the chat.
func TestStatuslineFrontStartsTheWorkerThenServesItsLine(t *testing.T) {
	jailTest(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_HOME", root)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, ".cc", "1"))
	t.Setenv("PFM_TMUX_DIR", filepath.Join(root, "tmux"))
	t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))
	t.Setenv("COLUMNS", "")
	var spawned []string
	original := statuslinePacedSpawn
	t.Cleanup(func() { statuslinePacedSpawn = original })
	statuslinePacedSpawn = func(key string) error {
		spawned = append(spawned, key)
		return nil
	}
	input := `{"session_id":"paced-front","model":{"display_name":"Opus 4"}}`
	key := statusline.PacedKey([]byte(input), "")
	dir := filepath.Join(root, "tmp", "cc-sl-paced-"+strconv.Itoa(os.Getuid()))
	runtime := statuslineTestRuntime(t)
	front := func() string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := runStatuslineWithRuntime(nil, strings.NewReader(input), &stdout, &stderr, runtime,
			paths.OSEnv{}); code != 0 || stderr.Len() != 0 {
			t.Fatalf("front code=%d stderr=%q", code, stderr.String())
		}
		return stdout.String()
	}

	first := front()
	if first != statusline.PacedPending || len(spawned) != 1 || spawned[0] != key {
		t.Fatalf("first tick stdout=%q spawned=%v; want the pending line and the chat's worker", first, spawned)
	}
	if _, err := os.Stat(filepath.Join(dir, key+".out.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cached line after the first tick: %v; want none, the worker renders it", err)
	}
	marker := "cached line \x1b[0m\n"
	if err := os.WriteFile(filepath.Join(dir, key+".out.json"),
		[]byte(`{"v":1,"output":"cached line \u001b[0m\n","finished_at":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, key+".lock"), os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if second := front(); second != marker || len(spawned) != 1 {
		t.Fatalf("second tick stdout=%q spawned=%v; want the cached line, no second worker", second, spawned)
	}
}

func TestStatuslinePacedWorkerFlagIsExclusiveAndRefusesABadKey(t *testing.T) {
	jailTest(t)
	t.Setenv("PFM_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := runStatuslineWithRuntime([]string{"--paced", "0123", "--subagents"}, strings.NewReader(""),
		&stdout, &stderr, statuslineTestRuntime(t), paths.OSEnv{}); code != 2 {
		t.Fatalf("--paced with --subagents code=%d stderr=%q; want usage 2", code, stderr.String())
	}
	stderr.Reset()
	if code := runStatuslineWithRuntime([]string{"--paced", "../escape"}, strings.NewReader(""),
		&stdout, &stderr, statuslineTestRuntime(t), paths.OSEnv{}); code != 1 ||
		!strings.Contains(stderr.String(), "32 hex digits") {
		t.Fatalf("--paced ../escape code=%d stderr=%q; want the key refused", code, stderr.String())
	}
}
