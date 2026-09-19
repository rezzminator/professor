package harvestpy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLockProvisionRootSerializesTwoHolders is half of L2-F23: nothing in the
// package held a lock, so two pfm processes converged the same environment
// tree at once.
func TestLockProvisionRootSerializesTwoHolders(t *testing.T) {
	root := filepath.Join(t.TempDir(), "env-browser", "linux-amd64")
	first, err := lockProvisionRoot(root)
	if err != nil {
		t.Fatalf("lockProvisionRoot() error = %v", err)
	}
	second := make(chan error, 1)
	go func() {
		release, err := lockProvisionRoot(root)
		if err == nil {
			err = release()
		}
		second <- err
	}()
	select {
	case err := <-second:
		t.Fatalf("a second holder took the lock while the first held it (err=%v)", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := first(); err != nil {
		t.Fatalf("release() error = %v", err)
	}
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("the waiting holder failed after the lock was released: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting holder never took the released lock")
	}
}

// TestProvisionWaitsForTheLock is the conversion-environment sibling of
// TestProvisionBrowserWaitsForTheLock below: Provision (the docling/pymupdf
// closure) took no lock at all — only the browser provisioner did — so two
// pfm processes converging the SAME conversion root at once each read a tree
// the other was halfway through replacing.
func TestProvisionWaitsForTheLock(t *testing.T) {
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	targets, cache := fakeProvisionInputs(t, root, platform)
	envRoot := filepath.Join(root, "env", platform.String())
	release, err := lockProvisionRoot(envRoot)
	if err != nil {
		t.Fatalf("lockProvisionRoot() error = %v", err)
	}
	reached := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		_, provisionErr := provisionWithTargets(context.Background(), ProvisionOptions{
			Root: root, Cache: cache, Platform: platform,
			Run: func(ctx context.Context, executable string, arguments []string, directory string) ([]byte, error) {
				select {
				case reached <- struct{}{}:
				default:
				}
				return fakeProvisionRun(t, false)(ctx, executable, arguments, directory)
			},
			Smoke: func(context.Context, Runtime) (map[string]any, error) {
				return map[string]any{
					"imports":    map[string]any{},
					"conversion": map[string]any{"ok": true},
				}, nil
			},
		}, targets)
		done <- provisionErr
	}()
	select {
	case <-reached:
		t.Fatal("Provision converged the tree while another holder had the root locked")
	case <-time.After(150 * time.Millisecond):
	}
	if err := release(); err != nil {
		t.Fatalf("release() error = %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("conversion provisioning failed after the lock was released: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("conversion provisioning never ran after the lock was released")
	}
}

// TestProvisionBrowserWaitsForTheLock: the provisioner itself must take it —
// a lock nothing calls is a lock that is not held.
func TestProvisionBrowserWaitsForTheLock(t *testing.T) {
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	targets, cache := fakeProvisionInputs(t, root, platform)
	envRoot := filepath.Join(root, "env-browser", platform.String())
	release, err := lockProvisionRoot(envRoot)
	if err != nil {
		t.Fatalf("lockProvisionRoot() error = %v", err)
	}
	reached := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		_, provisionErr := provisionBrowserWithTargets(context.Background(), ProvisionOptions{
			Root: root, Cache: cache, Platform: platform,
			Download: func(_ context.Context, url, _ string) error {
				return fmt.Errorf("unit provisioning hit the network (%s)", url)
			},
			Run: func(ctx context.Context, executable string, arguments []string, directory string) ([]byte, error) {
				select {
				case reached <- struct{}{}:
				default:
				}
				return fakeProvisionRun(t, false)(ctx, executable, arguments, directory)
			},
			Smoke: fakeBrowserSmoke(),
		}, targets)
		done <- provisionErr
	}()
	select {
	case <-reached:
		t.Fatal("ProvisionBrowser converged the tree while another holder had the root locked")
	case <-time.After(150 * time.Millisecond):
	}
	if err := release(); err != nil {
		t.Fatalf("release() error = %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("browser provisioning failed after the lock was released: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("browser provisioning never ran after the lock was released")
	}
}

// TestProvisionBrowserKeepsCurrentValidWhenTheRebuildDies is the other half of
// L2-F23: ProvisionBrowser did os.RemoveAll(final) at the path `current`
// already pointed to, so a crash — or a concurrent fetch — met a
// half-destroyed environment where a working one used to be.
func TestProvisionBrowserKeepsCurrentValidWhenTheRebuildDies(t *testing.T) {
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	targets, cache := fakeProvisionInputs(t, root, platform)
	options := func(run RunFunc, smoke SmokeFunc) ProvisionOptions {
		return ProvisionOptions{
			Root: root, Cache: cache, Platform: platform,
			Download: func(_ context.Context, url, _ string) error {
				return fmt.Errorf("unit provisioning hit the network (%s)", url)
			},
			Run:   run,
			Smoke: smoke,
		}
	}
	first, err := provisionBrowserWithTargets(
		context.Background(),
		options(fakeProvisionRun(t, false), fakeBrowserSmoke()),
		targets,
	)
	if err != nil {
		t.Fatalf("first browser provisioning failed: %v", err)
	}
	current := BrowserRuntimeRoot(root, platform)
	live, err := filepath.EvalSymlinks(current)
	if err != nil {
		t.Fatalf("current pointer does not resolve after the first provision: %v", err)
	}

	// Second run at the SAME digest — the repair path EnsureBrowser takes when
	// the live environment stops answering its smoke — and it dies partway
	// through the rebuild.
	_, err = provisionBrowserWithTargets(context.Background(), options(
		func(ctx context.Context, executable string, arguments []string, directory string) ([]byte, error) {
			if len(arguments) > 0 && arguments[0] == "sync" {
				return nil, errors.New("uv sync died mid-rebuild")
			}
			return fakeProvisionRun(t, false)(ctx, executable, arguments, directory)
		},
		func(context.Context, Runtime) (map[string]any, error) {
			return nil, errors.New("the live browser environment stopped answering its smoke")
		}), targets)
	if err == nil {
		t.Fatal("a failed rebuild reported success")
	}

	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		t.Fatalf("current pointer dangles after a failed rebuild: %v", err)
	}
	if resolved != live {
		t.Fatalf("current moved off the working environment during a failed rebuild: %s, was %s", resolved, live)
	}
	for _, relative := range []string{"environment.json", filepath.Join("project", "browser.py")} {
		if _, statErr := os.Stat(filepath.Join(current, relative)); statErr != nil {
			t.Fatalf("a failed rebuild destroyed the live environment's %s: %v", relative, statErr)
		}
	}
	if digest, inspectErr := InspectBrowser(root, platform); inspectErr != nil || digest.Digest != first.Digest {
		t.Fatalf("InspectBrowser after a failed rebuild = %v (digest %q, want %q)",
			inspectErr, digest.Digest, first.Digest)
	}
}

// TestProvisionBrowserRebuildSwapsAndClearsTheReplacedTree: a SUCCESSFUL
// same-digest rebuild leaves exactly one environment behind, not two.
func TestProvisionBrowserRebuildSwapsAndClearsTheReplacedTree(t *testing.T) {
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	targets, cache := fakeProvisionInputs(t, root, platform)
	build := func() (ProvisionResult, error) {
		return provisionBrowserWithTargets(context.Background(), ProvisionOptions{
			Root: root, Cache: cache, Platform: platform,
			Download: func(_ context.Context, url, _ string) error {
				return fmt.Errorf("unit provisioning hit the network (%s)", url)
			},
			Run: fakeProvisionRun(t, false),
			// A smoke that always refuses forces the reuse check to rebuild.
			Smoke: func(_ context.Context, runtime Runtime) (map[string]any, error) {
				if _, statErr := os.Stat(runtime.Script); statErr != nil {
					return nil, statErr
				}
				return map[string]any{"ok": true, "patchright": true, "chrome_path": "/usr/bin/google-chrome"}, nil
			},
		}, targets)
	}
	if _, err := build(); err != nil {
		t.Fatalf("first browser provisioning failed: %v", err)
	}
	envRoot := filepath.Join(root, "env-browser", platform.String())
	live, err := filepath.EvalSymlinks(BrowserRuntimeRoot(root, platform))
	if err != nil {
		t.Fatal(err)
	}
	// Break the live tree so the reuse check cannot short-circuit.
	if err := os.Remove(filepath.Join(live, "project", "browser.py")); err != nil {
		t.Fatal(err)
	}
	if _, err := build(); err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(BrowserRuntimeRoot(root, platform))
	if err != nil {
		t.Fatalf("current pointer dangles after a rebuild: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(resolved, "project", "browser.py")); statErr != nil {
		t.Fatalf("the rebuilt environment is missing its worker: %v", statErr)
	}
	entries, err := os.ReadDir(envRoot)
	if err != nil {
		t.Fatal(err)
	}
	environments := 0
	for _, entry := range entries {
		if entry.IsDir() {
			environments++
		}
	}
	if environments != 1 {
		t.Fatalf("environment directories after a rebuild = %d, want 1: %v", environments, entries)
	}
}

// fakeBrowserSmoke answers the browser provisioner's smoke without a process.
func fakeBrowserSmoke() SmokeFunc {
	return func(_ context.Context, runtime Runtime) (map[string]any, error) {
		if _, err := os.Stat(runtime.Script); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "patchright": true, "chrome_path": "/usr/bin/google-chrome"}, nil
	}
}
