package harvestmcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestFetchBrowserUsesConfiguredRunner closes F13: an MCP browser fetch must
// start its worker through the configured Runner, including when EnsureBrowser
// reuses an already provisioned environment. The fake worker is entirely an
// in-memory stdio protocol (newFakeBrowserWorker, browser_test.go); a real
// Python process must never be attempted.
func TestFetchBrowserUsesConfiguredRunner(t *testing.T) {
	converter, worker := newFakeBrowserWorker(t)
	html, status, _, err := converter.FetchBrowser(
		context.Background(),
		"https://93.184.216.34/f13",
	)
	if err != nil {
		t.Fatalf("FetchBrowser() error = %v", err)
	}
	if html != "fake browser result" || status != 200 {
		t.Fatalf("FetchBrowser() = (%q, %d), want fake response", html, status)
	}
	starts := worker.runner.Starts()
	if len(starts) != 1 {
		t.Fatalf("FakeRunner Start calls = %d, want 1: %+v", len(starts), starts)
	}
	if len(starts[0].Argv) != 2 || starts[0].Argv[0] != worker.python || starts[0].Argv[1] != worker.script {
		t.Fatalf("browser worker argv = %q, want [%q %q]", starts[0].Argv, worker.python, worker.script)
	}
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
