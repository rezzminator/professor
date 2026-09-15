package harvestpy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestProvisionBrowserConvergesUnderThePlatformRoot pins the review-cycle fix:
// the browser environment must land at env-browser/<GOOS>-<GOARCH>/<digest>
// with its current pointer beside it — never at the "-" sentinel an empty
// Platform{} stringifies to, and never one directory short. The venv
// interpreter symlink is absolute into the SAME tree, which also rules out a
// staging→final rename (it would dangle every link).
func TestProvisionBrowserConvergesUnderThePlatformRoot(t *testing.T) {
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	targets, cache := fakeProvisionInputs(t, root, platform)
	old := targetsForTest
	targetsForTest = targets
	t.Cleanup(func() { targetsForTest = old })

	var mu sync.Mutex
	smokes := 0
	result, err := provisionBrowser(context.Background(), ProvisionOptions{
		Root: root, Cache: cache, Platform: platform,
		Download: func(_ context.Context, url, _ string) error {
			t.Fatalf("unit provisioning hit the network (%s) — the targets seam is not injected", url)
			return fmt.Errorf("network refused in unit test")
		},
		Run: fakeProvisionRun(t, false),
		Smoke: func(_ context.Context, runtime Runtime) (map[string]any, error) {
			mu.Lock()
			defer mu.Unlock()
			smokes++
			resolved, err := filepath.EvalSymlinks(runtime.Python)
			if err != nil {
				return nil, err
			}
			if _, err := os.Stat(resolved); err != nil {
				return nil, err
			}
			if filepath.Base(runtime.Script) != "browser.py" {
				return nil, os.ErrInvalid
			}
			return map[string]any{"ok": true, "patchright": true, "chrome_path": "/usr/bin/google-chrome"}, nil
		},
	}, targets)
	if err != nil {
		t.Fatalf("browser provisioning failed: %v", err)
	}
	final := filepath.Join(root, "env-browser", platform.String(), result.Digest)
	venvPython := filepath.Join(final, "project", ".venv", "bin", "python")
	resolved, err := filepath.EvalSymlinks(venvPython)
	if err != nil {
		t.Fatalf("venv interpreter link dangles after provisioning: %v", err)
	}
	// The venv interpreter symlink is ABSOLUTE into the extracted tree, so it
	// must still resolve INSIDE this digest directory — a staging→final
	// rename would leave it dangling at the abandoned staging path.
	if info, statErr := os.Stat(resolved); statErr != nil || !info.Mode().IsRegular() {
		t.Fatalf(
			"interpreter symlink resolves outside/at %s (err=%v): a rename after uv sync dangles absolute links",
			resolved,
			statErr,
		)
	}
	if !strings.HasPrefix(resolved, final+string(filepath.Separator)) {
		t.Fatalf("interpreter resolved to %q, outside the digest root %q", resolved, final)
	}
	current := BrowserRuntimeRoot(root, platform)
	if _, err := os.Stat(filepath.Join(current, "project", "browser.py")); err != nil {
		t.Fatalf("provisioned worker script unreadable through current pointer: %v", err)
	}
	digest, err := InspectBrowser(root, platform)
	if err != nil || digest.Digest != result.Digest {
		t.Fatalf("InspectBrowser=%v digest=%q want %q", err, digest.Digest, result.Digest)
	}
	if smokes < 2 {
		t.Fatalf("smoke ran %d time(s), want staging AND final judged", smokes)
	}
}
