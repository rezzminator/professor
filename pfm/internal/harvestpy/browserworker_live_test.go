//go:build e2e

// LIVE network + real Chrome + ~100 MB provisioning. Never runs on a normal
// `go test ./...` — build it explicitly:
//
//	go test -tags e2e ./internal/harvestpy/ -run TestLiveBrowserWorkerFetch -v
//
// (review-2 S4: the feature's own enable flag is not a test gate; the repo
// convention for this tier is the e2e build tag, as in pfm/e2e.)
package harvestpy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"hostops/pfm/internal/harvest"
)

func TestLiveBrowserWorkerFetch(t *testing.T) {
	if os.Getenv("HARVESTER_BROWSER") != "1" {
		t.Skip("named gap: HARVESTER_BROWSER is not 1 — the opt-in real-browser rung is disabled on this host")
	}
	root := os.Getenv("PFM_HARVEST_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("named gap: no resolvable HOME (%v)", err)
		}
		root = filepath.Join(home, ".local", "state", "pfm", "harvest-python")
	}
	live := liveBrowserRuntime(root)
	if _, err := os.Stat(live.Python); err != nil {
		// The gate is ON, so provisioning here is exactly what production's
		// lazy first-use would do (no Chromium download either way).
		if _, provisionErr := ProvisionBrowser(
			context.Background(),
			ProvisionOptions{Root: root},
		); provisionErr != nil {
			t.Skipf(
				"named gap: browser environment not provisioned at %s and lazy provisioning failed (%v)",
				live.Python,
				provisionErr,
			)
		}
	}
	interpreter, script := live.Python, live.Script
	worker := NewBrowserWorker(Runtime{Python: interpreter, Script: script})
	defer func() { _ = worker.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 90_000_000_000)
	defer cancel()
	smoke, err := worker.Smoke(ctx)
	if err != nil {
		t.Fatalf("live smoke failed: %v", err)
	}
	chromePath, _ := smoke["chrome_path"].(string)
	if chromePath == "" {
		t.Skip("named gap: no system Chrome binary resolves on this host")
	}
	html, status, err := worker.Fetch(ctx, "https://example.com/", "", true, 45000, func(url string) error {
		return harvest.AssertFetchableStrict(url)
	})
	if err != nil {
		t.Fatalf("live browser fetch failed: %v", err)
	}
	if status != 200 || len(html) < 100 {
		t.Fatalf("live browser fetch returned thin content: status=%d bytes=%d", status, len(html))
	}
}
