package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
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
	code := run([]string{"--config", configPath, "statusline"}, &stdout, &stderr)
	if got, want := rooted(
		stdout.String(),
		root,
	), "\x1b[0m\x1b[38;5;51m◆ Opus 4\x1b[0m\x1b[38;5;245m·\x1b[0m\x1b[38;5;220meffort unknown\x1b[0m \x1b[2m│\x1b[0m \x1b[1;34m~\x1b[0m \x1b[2m│\x1b[0m \x1b[2;90m·\x1b[m \x1b[2;90m·\x1b[m\n\x1b[0m🟢 \x1b[1;32m\x1b[2m▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱\x1b[0m \x1b[1;32m?% ?\x1b[0m \x1b[38;5;226mctx unknown\x1b[0m \x1b[2m│\x1b[0m \x1b[38;5;226m💰$?\x1b[0m\x1b[38;5;245m/\x1b[0m\x1b[38;5;226m?/?/?\x1b[0m \x1b[2m│\x1b[0m \x1b[1;38;5;203m💾⚠config\x1b[0m \x1b[2m│\x1b[0m \x1b[38;5;215m⏳ 0s\x1b[0m\n\x1b[0m\x1b[2m▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱\x1b[0m \x1b[2mcodex-7d: no data\x1b[0m\n"; code != 0 ||
		got != want {
		t.Fatalf("broken config statusline code=%d stderr=%q stdout = %q, want %q", code, stderr.String(), got, want)
	}
}

// The usage hook runs on defaults and names the broken config, while a
// command that edits the config still refuses and leaves the file as written.
func TestUsageHookOnABrokenConfigRunsOnDefaults(t *testing.T) {
	root := jailTest(t)
	configPath := writeConfigFixture(t, root, "{")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--config", configPath, "usage-hook"}, &stdout, &stderr)
	first, _, _ := strings.Cut(stderr.String(), "\n")
	want := "pfm: config: parse config " + configPath +
		" at byte 1: unexpected EOF; running on defaults until it is fixed (pfm doctor)"
	if code != 0 || first != want {
		t.Fatalf("broken config usage-hook code=%d first stderr line=%q, want 0 and %q (stdout=%q stderr=%q)",
			code, first, want, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--config", configPath, mcpCommand, "chat", "disable"}, &stdout, &stderr); code != 1 {
		t.Fatalf("broken config mcp chat disable code=%d stdout=%q stderr=%q, want 1", code, stdout.String(),
			stderr.String())
	}
	if got := string(mustReadFile(t, configPath)); got != "{" {
		t.Fatalf("broken config %s after mcp chat disable = %q, want %q", configPath, got, "{")
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
	if code := runStatuslineWithRuntime(
		nil,
		strings.NewReader("{"),
		&stdout,
		&stderr,
		statuslineTestRuntime(
			t,
		),
		paths.OSEnv{},
	); code != 0 || stderr.String() != "pfm statusline: render (fail-open): decode statusline input: unexpected end of JSON input\n" {
		t.Fatalf(
			"malformed statusline: code=%d stdout=%q stderr = %q, want %q",
			code,
			stdout.String(),
			stderr.String(),
			"pfm statusline: render (fail-open): decode statusline input: unexpected end of JSON input\n",
		)
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
	); code != 0 || stdout.Len() != 0 || rooted(stderr.String(), root) != "pfm usage-hook: evaluate (fail-open): usage cache directory {root}/tmp/"+filepath.Base(cacheLink)+" is not a real directory\n" {
		t.Fatalf(
			"usage hook: code=%d stdout=%q stderr = %q, want %q",
			code,
			stdout.String(),
			rooted(stderr.String(), root),
			"pfm usage-hook: evaluate (fail-open): usage cache directory {root}/tmp/cc-usage-0 is not a real directory\n",
		)
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
	t.Setenv("TMUX", "/tmp/tmux-1/cc-front,1,0")
	t.Setenv("TMUX_PANE", "%3")
	t.Setenv("HERDR_SOCKET_PATH", "")
	t.Setenv("HERDR_PANE_ID", "")
	var spawned []string
	original := statuslinePacedSpawn
	t.Cleanup(func() { statuslinePacedSpawn = original })
	statuslinePacedSpawn = func(key string) error {
		spawned = append(spawned, key)
		return nil
	}
	input := `{"session_id":"paced-front","model":{"display_name":"Opus 4"}}`
	pane := resolve.WhoamiEnvironment{TMUX: "/tmp/tmux-1/cc-front,1,0", TMUXPane: "%3"}
	key := statusline.PacedKey([]byte(input), "", filepath.Join(root, ".cc", "1"), pane)
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
	// The chat resumed on account 2: the account-1 worker still holds its lock,
	// and the tick goes to a new worker rather than serving account 1's line.
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, ".cc", "2"))
	moved := statusline.PacedKey([]byte(input), "", filepath.Join(root, ".cc", "2"), pane)
	if third := front(); third != statusline.PacedPending || len(spawned) != 2 || spawned[1] != moved ||
		moved == key {
		t.Fatalf("tick after the account move stdout=%q spawned=%v key=%s; want the pending line and worker %s",
			third, spawned, key, moved)
	}
}

func TestStatuslinePacedWorkerFlagIsExclusiveAndRefusesABadKey(t *testing.T) {
	jailTest(t)
	t.Setenv("PFM_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := runStatuslineWithRuntime([]string{"--paced", "0123", "--refresh-gpt"}, strings.NewReader(""),
		&stdout, &stderr, statuslineTestRuntime(t), paths.OSEnv{}); code != 2 {
		t.Fatalf("--paced with --refresh-gpt code=%d stderr=%q; want usage 2", code, stderr.String())
	}
	stderr.Reset()
	if code := runStatuslineWithRuntime([]string{"--paced", "../escape"}, strings.NewReader(""),
		&stdout, &stderr, statuslineTestRuntime(t), paths.OSEnv{}); code != 1 ||
		!strings.Contains(stderr.String(), "32 hex digits") {
		t.Fatalf("--paced ../escape code=%d stderr=%q; want the key refused", code, stderr.String())
	}
}

func TestStatuslineWarningsReachTheActivityLog(t *testing.T) {
	jailTest(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_HOME", root)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, ".cc", "1"))
	t.Setenv("PFM_TMUX_DIR", filepath.Join(root, "tmux"))
	t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))
	stateDir := filepath.Join(root, "auto-effort")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.sess-w.json"), []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUTO_EFFORT_STATE_FILE", filepath.Join(stateDir, "state.json"))
	// No worker in this test: the front renders inline, through the runtime the
	// detached worker renders with, whose stderr nobody reads.
	original := statuslinePacedSpawn
	t.Cleanup(func() { statuslinePacedSpawn = original })
	statuslinePacedSpawn = func(string) error { return errors.New("no worker in this test") }
	_, recorder := obs.Test(t)

	var stdout, stderr bytes.Buffer
	code := runStatuslineWithRuntime(
		nil,
		strings.NewReader(`{"session_id":"sess-w","model":{"display_name":"Opus 4"}}`),
		&stdout,
		&stderr,
		statuslineTestRuntime(t),
		paths.OSEnv{},
	)
	if got, want := rooted(
		stdout.String(),
		root,
	), "\x1b[0m\x1b[38;5;51m◆ Opus 4\x1b[0m\x1b[38;5;245m·\x1b[0m\x1b[1;38;5;203meffort unknown · ERR\x1b[0m \x1b[2m│\x1b[0m \x1b[1;34m~\x1b[0m \x1b[2m│\x1b[0m \x1b[2;90m·\x1b[m \x1b[2;90m·\x1b[m\n\x1b[0m🟢 \x1b[1;32m\x1b[2m▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱\x1b[0m \x1b[1;32m?% ?\x1b[0m \x1b[38;5;226mctx unknown\x1b[0m \x1b[2m│\x1b[0m \x1b[38;5;226m💰$?\x1b[0m\x1b[38;5;245m/\x1b[0m\x1b[38;5;226m?/?/?\x1b[0m \x1b[2m│\x1b[0m \x1b[38;5;215m⏳ 0s\x1b[0m\n\x1b[0m\x1b[2m▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱\x1b[0m \x1b[2mcodex-7d: no data\x1b[0m\n"; code != 0 ||
		got != want {
		t.Fatalf("code=%d stderr=%q stdout = %q, want %q", code, stderr.String(), got, want)
	}
	var causes []string
	for _, record := range recorder.Records() {
		if cause, _ := record.Field(obs.FieldErr); record.Message == "statusline.warn" && record.Level == "WARN" {
			causes = append(causes, scrub(fmt.Sprint(cause), root))
		}
	}
	if got, want := fmt.Sprintf(
		"%q",
		causes,
	), "[\"pfm statusline: paced render (rendering inline): spawn paced worker {hash}: no worker in this test\" \"statusline: auto-effort state {root}/auto-effort/state.sess-w.json (effort shows ERR): parse \\\"not json\\\": invalid character 'o' in literal null (expecting 'u')\"]"; got != want {
		t.Fatalf("statusline.warn causes = %q, want %q", got, want)
	}
}
