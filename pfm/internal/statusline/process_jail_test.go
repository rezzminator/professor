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
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// appServerFixtureEnv marks the re-exec of this test binary that plays the
// Codex App Server (TestCodexAppServerFixture).
const appServerFixtureEnv = "PFM_GPT_APP_SERVER_FIXTURE"

func TestAppServerHandshakeCarriesJSONRPCAndReadsIDOne(t *testing.T) {
	command := exec.CommandContext(
		context.Background(),
		os.Args[0],
		"-test.run=TestCodexAppServerFixture",
		"--",
		"app-server",
	)
	command.Env = append(os.Environ(), appServerFixtureEnv+"=1")
	body, err := readCodexRateLimitsCommand(context.Background(), command)
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
	// The real App Server cancels outstanding work when stdin reaches EOF.
	// Refuse to answer a parent that closes the transport immediately after
	// writing the request: that is the production failure this fixture pins.
	eof := make(chan error, 1)
	go func() {
		_, err := reader.ReadByte()
		eof <- err
	}()
	select {
	case err := <-eof:
		if err == io.EOF {
			os.Exit(94)
		}
	case <-time.After(100 * time.Millisecond):
	}
	_, _ = os.Stdout.WriteString(`{"jsonrpc":"2.0","method":"configWarning","params":{}}` + "\n")
	_, _ = os.Stdout.WriteString(
		`{"jsonrpc":"2.0","id":1,"result":{"rateLimits":{"primary":{"usedPercent":10},"planType":"plus"}}}` + "\n",
	)
	time.Sleep(10 * time.Second)
	os.Exit(0)
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
