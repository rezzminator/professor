package statusline

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppServerHandshakeCarriesJSONRPCAndReadsIDOne(t *testing.T) {
	command := exec.CommandContext(
		context.Background(),
		os.Args[0],
		"-test.run=TestCodexAppServerFixture",
		"--",
		"app-server",
	)
	command.Env = append(os.Environ(), "PFM_GPT_APP_SERVER_FIXTURE=1")
	body, err := readCodexRateLimitsCommand(command)
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
	if err := os.WriteFile(codex, []byte(script), 0o700); err != nil {
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
	if !strings.Contains(string(first), firstHome) || strings.Contains(string(first), secondHome) {
		t.Fatalf("first response leaked CODEX_HOME: %s", first)
	}
	if !strings.Contains(string(second), secondHome) || strings.Contains(string(second), firstHome) {
		t.Fatalf("second response leaked CODEX_HOME: %s", second)
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
	_, err := readCodexRateLimitsCommand(command)
	if err == nil || !strings.Contains(err.Error(), "no id=1 response") {
		t.Fatalf("wrong-id App Server response error=%v, want named missing id=1 failure", err)
	}
}

func TestCodexAppServerFixture(t *testing.T) {
	if os.Getenv("PFM_GPT_APP_SERVER_FIXTURE") != "1" {
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
