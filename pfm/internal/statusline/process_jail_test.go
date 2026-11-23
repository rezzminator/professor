//go:build jail

package statusline

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// appServerFixtureEnv marks the re-exec of this test binary that plays the
// Codex App Server (TestCodexAppServerFixture).
const appServerFixtureEnv = "PFM_GPT_APP_SERVER_FIXTURE"

// appServerReleaseFD is the descriptor the App Server fixture inherits its
// release pipe on (the first of the command's extra files): the fixture answers
// once that pipe reaches its end, which the test brings by closing the write
// end it kept. An inherited pipe needs no open on either side, so the release
// depends on no meeting of a reader and a writer at a path.
const appServerReleaseFD = 3

// readFromAppServerFixture runs readCodexRateLimitsCommand against the App
// Server fixture, and releases the fixture to answer once the parent is
// blocked reading the answer: a parent that closed the transport right after
// its request has closed it by then, and the fixture sees its EOF.
func readFromAppServerFixture(t *testing.T, ctx context.Context) ([]byte, error) {
	t.Helper()
	held, release, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = held.Close()
		_ = release.Close()
	})
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCodexAppServerFixture", "--", "app-server")
	command.Env = append(os.Environ(), appServerFixtureEnv+"=1")
	command.ExtraFiles = []*os.File{held}
	type read struct {
		body []byte
		err  error
	}
	done := make(chan read, 1)
	go func() {
		body, err := readCodexRateLimitsCommand(ctx, command)
		done <- read{body, err}
	}()
	waited := false
	testwait.Until(t, "the parent to wait on the App Server's answer", func() (bool, string) {
		select {
		case got := <-done:
			done <- got
			return true, "the read returned"
		default:
		}
		blocked, state := testwait.Blocked("bufio.(*Scanner).Scan", "statusline.readCodexRateLimitsCommand")
		waited = blocked
		return blocked, state
	})
	if err := release.Close(); err != nil {
		t.Fatalf("release the App Server fixture: %v", err)
	}
	got, _ := testwait.Receive(t, "the App Server read to return", done)
	if got.err != nil && !waited {
		t.Logf("the read returned before the parent was seen waiting on the App Server's answer")
	}
	return got.body, got.err
}

func TestAppServerHandshakeCarriesJSONRPCAndReadsIDOne(t *testing.T) {
	body, err := readFromAppServerFixture(t, testwait.Context(t))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseCodexRateLimits(body)
	if err != nil || parsed["planType"] != "plus" {
		t.Fatalf("parsed=%#v err=%v", parsed, err)
	}
}

func TestReadCodexRateLimitsWithBinaryAtHomeIsolatesAccounts(t *testing.T) {
	binDir := t.TempDir()
	codex := filepath.Join(binDir, "codex")
	script := `#!/bin/sh
count=0
while IFS= read -r line; do
  count=$((count + 1))
  if [ "$count" -eq 3 ]; then
    printf '{"jsonrpc":"2.0","id":1,"result":{"rateLimits":{"planType":"%s","primary":{"usedPercent":10}}}}\n' "$CODEX_HOME"
    exit 0
  fi
done
exit 2
`
	if err := testjail.WriteExecutable(codex, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	firstHome := filepath.Join(t.TempDir(), "codex-one")
	secondHome := filepath.Join(t.TempDir(), "codex-two")

	first, err := ReadCodexRateLimitsWithBinaryAtHome(context.Background(), "codex", firstHome)
	if err != nil {
		t.Fatalf("first App Server read: %v", err)
	}
	second, err := ReadCodexRateLimitsWithBinaryAtHome(context.Background(), "codex", secondHome)
	if err != nil {
		t.Fatalf("second App Server read: %v", err)
	}
	got := strings.NewReplacer(firstHome, "{first}", secondHome, "{second}").
		Replace(string(first) + "\n" + string(second))
	if want := "{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"rateLimits\":{\"planType\":\"{first}\",\"primary\":{\"usedPercent\":10}}}}\n\n{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"rateLimits\":{\"planType\":\"{second}\",\"primary\":{\"usedPercent\":10}}}}\n"; got != want {
		t.Fatalf("responses = %q, want %q", got, want)
	}
}

// TestReadCodexRateLimitsDropsTheHerdrPane: the App Server read runs no turn,
// so it is never the chat bound to the parent's Herdr pane — with or without an
// explicit account home, none of the four Herdr names reaches it.
func TestReadCodexRateLimitsDropsTheHerdrPane(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/h.sock")
	t.Setenv("HERDR_BIN_PATH", "/usr/local/bin/herdr")
	t.Setenv("PFM_PROBE_KEEP", "kept")
	for _, codexHome := range []string{"", filepath.Join(t.TempDir(), "codex-one")} {
		t.Run("home="+filepath.Base(codexHome), func(t *testing.T) {
			binDir := t.TempDir()
			record := filepath.Join(binDir, "env.log")
			script := `#!/bin/sh
/usr/bin/env > '` + record + `'
count=0
while IFS= read -r line; do
  count=$((count + 1))
  if [ "$count" -eq 3 ]; then
    printf '{"jsonrpc":"2.0","id":1,"result":{"rateLimits":{"planType":"plus","primary":{"usedPercent":10}}}}\n'
    exit 0
  fi
done
exit 2
`
			if err := testjail.WriteExecutable(filepath.Join(binDir, "codex"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			if _, err := ReadCodexRateLimitsWithBinaryAtHome(context.Background(), "codex", codexHome); err != nil {
				t.Fatalf("App Server read: %v", err)
			}
			raw, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			env := map[string]string{}
			for _, line := range strings.Split(string(raw), "\n") {
				name, value, _ := strings.Cut(line, "=")
				env[name] = value
			}
			got := ""
			for _, name := range []string{"PFM_PROBE_KEEP", "HERDR_ENV", "HERDR_PANE_ID", "HERDR_SOCKET_PATH", "HERDR_BIN_PATH"} {
				value, set := env[name]
				got += fmt.Sprintf("%s=%q/%t ", name, value, set)
			}
			if want := `PFM_PROBE_KEEP="kept"/true HERDR_ENV=""/false HERDR_PANE_ID=""/false HERDR_SOCKET_PATH=""/false HERDR_BIN_PATH=""/false `; got != want {
				t.Fatalf("App Server environment = %q, want %q", got, want)
			}
		})
	}
}

func TestReadCodexRateLimitsRejectsMalformedOrWrongIDResponses(t *testing.T) {
	command := exec.Command("sh", "-c", `
while IFS= read -r line; do
  count=$((count + 1))
  if [ "$count" -eq 3 ]; then
    printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"rateLimits":{"primary":{}}}}'
    exit 0
  fi
done
`)
	_, err := readCodexRateLimitsCommand(context.Background(), command)
	if got, want := fmt.Sprint(
		err,
	), "command from App Server returned no id=1 response: wait=<nil> stderr="; got != want {
		t.Fatalf("wrong-id App Server response error = %q, want %q", got, want)
	}
}

func TestCodexAppServerFixture(t *testing.T) {
	if os.Getenv(appServerFixtureEnv) != "1" {
		t.Skip("Codex App Server fixture runs only as a helper process")
	}
	appServerFixtureStage("started")
	reader := bufio.NewReader(os.Stdin)
	lines := make([]string, 0, 3)
	for len(lines) < 3 {
		line, err := reader.ReadString('\n')
		if err != nil {
			os.Exit(91)
		}
		lines = append(lines, strings.TrimSpace(line))
	}
	for _, line := range lines {
		var message map[string]any
		if json.Unmarshal([]byte(line), &message) != nil || message["jsonrpc"] != "2.0" {
			os.Exit(93)
		}
	}
	appServerFixtureStage("handshake read, waiting for the release")
	// The real App Server cancels outstanding work when stdin reaches EOF.
	// Refuse to answer a parent that closes the transport immediately after
	// writing the request: that is the production failure this fixture pins.
	// The test releases this fixture once the parent waits on the answer, so
	// such a parent's EOF is queued by then.
	release := os.NewFile(appServerReleaseFD, "release")
	if _, err := io.Copy(io.Discard, release); err != nil {
		os.Exit(95)
	}
	_ = release.Close()
	appServerFixtureStage("released")
	if reader.Buffered() == 0 && appServerStdinClosed() {
		os.Exit(94)
	}
	_, _ = os.Stdout.WriteString(`{"jsonrpc":"2.0","method":"configWarning","params":{}}` + "\n")
	_, _ = os.Stdout.WriteString(
		`{"jsonrpc":"2.0","id":1,"result":{"rateLimits":{"primary":{"usedPercent":10},"planType":"plus"}}}` + "\n",
	)
	// Stay up until the parent ends the session by closing stdin, or kills this.
	for {
		if _, err := reader.ReadByte(); err != nil {
			break
		}
	}
	os.Exit(0)
}

// appServerFixtureStage writes the stage the fixture reached to its stderr,
// which the parent's error quotes: a fixture killed before it answered names
// where it stood.
func appServerFixtureStage(stage string) {
	_, _ = os.Stderr.WriteString("app server fixture: " + stage + "\n")
}

// appServerStdinClosed reads stdin once without blocking: true when it is at
// EOF, false while the parent holds it open. Stdin blocks again after.
func appServerStdinClosed() bool {
	if err := syscall.SetNonblock(0, true); err != nil {
		os.Exit(96)
	}
	defer func() { _ = syscall.SetNonblock(0, false) }()
	var probe [1]byte
	n, err := syscall.Read(0, probe[:])
	return n == 0 && err == nil
}

// TestReadCodexRateLimitsNamesAMissingBinary: a Codex binary absent from
// PATH fails at resolution with its name, never as a later exec error.
func TestReadCodexRateLimitsNamesAMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := ReadCodexRateLimitsWithBinaryAtHome(context.Background(), "codex", "")
	want := `resolve Codex binary "codex": exec: "codex": executable file not found in $PATH`
	if err == nil || err.Error() != want {
		t.Fatalf("ReadCodexRateLimitsWithBinaryAtHome() error = %v, want %q", err, want)
	}
}
