package harvestmcp

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goRuntime "runtime"
	"testing"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/harvestpy"
)

// TestFetchBrowserUsesConfiguredRunner closes F13: an MCP browser fetch must
// start its worker through the configured Runner, including when EnsureBrowser
// reuses an already provisioned environment. The fake worker is entirely an
// in-memory stdio protocol; a real Python process must never be attempted.
func TestFetchBrowserUsesConfiguredRunner(t *testing.T) {
	root := t.TempDir()
	platform := harvestpy.Platform{GOOS: goRuntime.GOOS, GOARCH: goRuntime.GOARCH}
	current := harvestpy.BrowserRuntimeRoot(root, platform)
	python := filepath.Join(current, "project", ".venv", "bin", "python")
	script := filepath.Join(current, "project", "browser.py")
	if err := os.MkdirAll(filepath.Dir(python), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{python, script} {
		if err := os.WriteFile(path, []byte("fixture"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	record := harvestpy.EnvironmentDigest{
		Schema:       1,
		SourceSHA256: sha256Hex(harvestpy.BrowserWorkerSource()),
		LockSHA256:   sha256Hex(harvestpy.BrowserLockMetadata()),
	}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, "environment.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	runner := &deps.FakeRunner{}
	runner.ScriptInteractive([]string{python}, deps.InteractiveScript{
		Pid:    9101,
		Stdin:  stdinWriter,
		Stdout: stdoutReader,
	})
	go func() {
		defer func() { _ = stdoutWriter.Close() }()
		if _, err := bufio.NewReader(stdinReader).ReadString('\n'); err != nil {
			return
		}
		_, _ = fmt.Fprintln(stdoutWriter, `{"ok":true,"html":"fake browser result","status":200}`)
	}()

	converter := pythonConverter{
		browserRoot: root,
		runner:      runner,
	}
	html, status, err := converter.FetchBrowser(
		context.Background(),
		"https://93.184.216.34/f13",
		true,
	)
	if err != nil {
		t.Fatalf("FetchBrowser() error = %v", err)
	}
	if html != "fake browser result" || status != 200 {
		t.Fatalf("FetchBrowser() = (%q, %d), want fake response", html, status)
	}
	starts := runner.Starts()
	if len(starts) != 1 {
		t.Fatalf("FakeRunner Start calls = %d, want 1: %+v", len(starts), starts)
	}
	if len(starts[0].Argv) != 2 || starts[0].Argv[0] != python || starts[0].Argv[1] != script {
		t.Fatalf("browser worker argv = %q, want [%q %q]", starts[0].Argv, python, script)
	}
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
