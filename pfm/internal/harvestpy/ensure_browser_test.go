package harvestpy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// writeBrowserEnv lays down an environment whose interpreter exists and whose
// record carries the given worker source and lock hashes.
func writeBrowserEnv(t *testing.T, root, sourceSHA, lockSHA string) Runtime {
	t.Helper()
	current := BrowserRuntimeRoot(root, Platform{GOOS: goruntime.GOOS, GOARCH: goruntime.GOARCH})
	python := filepath.Join(current, "project", ".venv", "bin", "python")
	if err := os.MkdirAll(filepath.Dir(python), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(python, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(
		EnvironmentDigest{Schema: 1, State: "ready", SourceSHA256: sourceSHA, LockSHA256: lockSHA},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, "environment.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	return Runtime{Python: python, Script: filepath.Join(current, "project", "browser.py")}
}

func countingProvision(t *testing.T) *int {
	t.Helper()
	calls := 0
	previous := ensureProvision
	ensureProvision = func(context.Context, ProvisionOptions) (ProvisionResult, error) {
		calls++
		return ProvisionResult{}, nil
	}
	t.Cleanup(func() { ensureProvision = previous })
	return &calls
}

// TestEnsureBrowserReprovisionsAWorkerFromAnOlderPfm pins the upgrade gap:
// an environment an older pfm provisioned still has its interpreter, so the
// "missing interpreter" check alone kept running the OLD browser.py — a
// changed worker protocol shipped and silently never ran.
func TestEnsureBrowserReprovisionsAWorkerFromAnOlderPfm(t *testing.T) {
	root := t.TempDir()
	calls := countingProvision(t)
	writeBrowserEnv(t, root, "worker-from-an-older-pfm", browserLockSHA256())
	if _, err := EnsureBrowser(context.Background(), ProvisionOptions{Root: root}); err != nil {
		t.Fatalf("EnsureBrowser: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("stale browser worker provisioned %d time(s), want 1", *calls)
	}
}

// TestEnsureBrowserReusesACurrentEnvironment: a current record costs no
// provisioning — EnsureBrowser runs on every browser fetch.
func TestEnsureBrowserReusesACurrentEnvironment(t *testing.T) {
	root := t.TempDir()
	calls := countingProvision(t)
	want := writeBrowserEnv(t, root, browserSourceSHA256(), browserLockSHA256())
	got, err := EnsureBrowser(context.Background(), ProvisionOptions{Root: root})
	if err != nil {
		t.Fatalf("EnsureBrowser: %v", err)
	}
	if *calls != 0 || got != want {
		t.Fatalf("current environment: provisioned %d time(s), runtime=%+v want %+v", *calls, got, want)
	}
}

// TestEnsureBrowserProvisionsAMissingEnvironmentUnderTheNormalizedPlatform
// keeps the earlier review finding: the runtime lives under
// env-browser/<GOOS>-<GOARCH>/, never the "-" an empty Platform{} renders.
func TestEnsureBrowserProvisionsAMissingEnvironmentUnderTheNormalizedPlatform(t *testing.T) {
	root := t.TempDir()
	calls := countingProvision(t)
	got, err := EnsureBrowser(context.Background(), ProvisionOptions{Root: root})
	if err != nil {
		t.Fatalf("EnsureBrowser: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("missing environment provisioned %d time(s), want 1", *calls)
	}
	wantRoot := BrowserRuntimeRoot(root, Platform{GOOS: goruntime.GOOS, GOARCH: goruntime.GOARCH})
	if got.Python != filepath.Join(wantRoot, "project", ".venv", "bin", "python") ||
		strings.Contains(got.Python, string(filepath.Separator)+"-"+string(filepath.Separator)) {
		t.Fatalf("runtime %+v not under the normalized platform root %q", got, wantRoot)
	}
}

func TestBrowserEnvironmentStaleNamesTheDrift(t *testing.T) {
	if reason := BrowserEnvironmentStale(
		EnvironmentDigest{SourceSHA256: browserSourceSHA256(), LockSHA256: browserLockSHA256()},
	); reason != "" {
		t.Fatalf("current record reported stale: %q", reason)
	}
	reason := BrowserEnvironmentStale(EnvironmentDigest{SourceSHA256: "old", LockSHA256: "old"})
	if !strings.Contains(reason, "worker source") || !strings.Contains(reason, "dependency lock") {
		t.Fatalf("drift not named: %q", reason)
	}
}
