package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeLauncherInstallDisplacementAndRepair(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, ".local", "bin", "claude")
	nativeOne := filepath.Join(home, ".local", "share", "claude", "versions", "1.0.0")
	nativeTwo := filepath.Join(home, ".local", "share", "claude", "versions", "2.0.0")
	for _, binary := range []string{nativeOne, nativeTwo} {
		if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(nativeOne, canonical); err != nil {
		t.Fatal(err)
	}

	apply := func() {
		t.Helper()
		if _, err := Run(context.Background(), Options{
			Mode: ModeApply, Home: home, Runner: &fakeRunner{},
		}); err != nil {
			t.Fatal(err)
		}
	}
	apply()
	managed := filepath.Join(home, ".local", "share", "pfm", "install", "bin", "claude")
	assertLink(t, canonical, managed)
	content, err := os.ReadFile(filepath.Join(home, ".local", "share", "pfm", "install", "launcher.state"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), nativeOne) {
		t.Fatalf("launcher.state=%q, want displaced target %q", content, nativeOne)
	}

	if err := os.Remove(canonical); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(nativeTwo, canonical); err != nil {
		t.Fatal(err)
	}
	status, err := InspectClaudeLauncher(home)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != LauncherDisplaced || status.Target != nativeTwo {
		t.Fatalf("displaced status=%#v, want target %s", status, nativeTwo)
	}

	apply()
	assertLink(t, canonical, managed)
	if changed, err := RepairClaudeLauncher(home); err != nil || changed {
		t.Fatalf("idempotent repair changed=%t err=%v", changed, err)
	}
	if err := os.Remove(canonical); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(nativeTwo, canonical); err != nil {
		t.Fatal(err)
	}
	if changed, err := RepairClaudeLauncher(home); err != nil || !changed {
		t.Fatalf("displaced repair changed=%t err=%v", changed, err)
	}
	assertLink(t, canonical, managed)
}

func TestClaudeLauncherAssetIsExactExecShim(t *testing.T) {
	raw, err := readAsset("bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	want := "#!/bin/sh\n" + `exec "$HOME/.local/bin/pfm" internal claude-launch "$@"` + "\n"
	if string(raw) != want {
		t.Fatalf("bin/claude=%q, want exact exec shim %q", raw, want)
	}
	assets, err := assetFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if asset.path == "bin/claude" {
			if asset.mode != 0o755 {
				t.Fatalf("bin/claude mode=%#o, want 0755", asset.mode)
			}
			return
		}
	}
	t.Fatal("bin/claude missing from staged assets")
}

func TestAssetRenderersRefuseMissingTemplateMarkers(t *testing.T) {
	if _, err := renderShimAsset([]byte("marker drift\n"), Options{}); err == nil {
		t.Fatal("shim renderer silently accepted missing markers")
	}
}

func TestResolveClaudeBinaryUsesConfiguredThenNewestThenPATH(t *testing.T) {
	home := t.TempDir()
	versions := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	newest := filepath.Join(versions, "2.1.270")
	writeExecutable(t, newest)
	pathDir := filepath.Join(home, "path-bin")
	if err := os.MkdirAll(pathDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pathBinary := filepath.Join(pathDir, "claude")
	writeExecutable(t, pathBinary)
	configured := filepath.Join(home, "configured-claude")
	writeExecutable(t, configured)

	resolved, err := ResolveClaudeBinary(home, configured, pathDir)
	if err != nil || resolved != configured {
		t.Fatalf("configured resolution=%q err=%v, want %q", resolved, err, configured)
	}
	if err := os.Chmod(configured, 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err = ResolveClaudeBinary(home, configured, pathDir)
	if err != nil || resolved != newest {
		t.Fatalf("version resolution=%q err=%v, want %q", resolved, err, newest)
	}
	if err := os.Remove(newest); err != nil {
		t.Fatal(err)
	}
	resolved, err = ResolveClaudeBinary(home, configured, pathDir)
	if err != nil || resolved != pathBinary {
		t.Fatalf("PATH resolution=%q err=%v, want %q", resolved, err, pathBinary)
	}
}

func TestResolveClaudeBinaryUsesRelativeConfiguredCommandFromSuppliedPATH(t *testing.T) {
	home := t.TempDir()
	versions := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	newest := filepath.Join(versions, "9.9.9")
	writeExecutable(t, newest)
	pathDir := filepath.Join(home, "path-bin")
	if err := os.MkdirAll(pathDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configured := filepath.Join(pathDir, "claude-nightly")
	writeExecutable(t, configured)

	resolved, err := ResolveClaudeBinary(home, "claude-nightly", pathDir)
	if err != nil || resolved != configured {
		t.Fatalf("relative configured resolution=%q err=%v, want %q", resolved, err, configured)
	}
}

func TestResolveClaudeBinaryMissingRelativeConfiguredCommandFails(t *testing.T) {
	home := t.TempDir()
	newest := filepath.Join(home, ".local", "share", "claude", "versions", "9.9.9")
	if err := os.MkdirAll(filepath.Dir(newest), 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, newest)
	pathDir := filepath.Join(home, "path-bin")
	if err := os.MkdirAll(pathDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(pathDir, "claude"))

	resolved, err := ResolveClaudeBinary(home, "claude-nightly", pathDir)
	if err == nil || resolved != "" || !strings.Contains(err.Error(), "claude-nightly") {
		t.Fatalf(
			"missing relative configured resolution=%q err=%v, want an error naming claude-nightly without fallback",
			resolved,
			err,
		)
	}
}

func TestResolveClaudeBinaryDefaultNameStillUsesNativeFallback(t *testing.T) {
	home := t.TempDir()
	newest := filepath.Join(home, ".local", "share", "claude", "versions", "9.9.9")
	if err := os.MkdirAll(filepath.Dir(newest), 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, newest)

	resolved, err := ResolveClaudeBinary(home, "claude", t.TempDir())
	if err != nil || resolved != newest {
		t.Fatalf("default-name resolution=%q err=%v, want native fallback %q", resolved, err, newest)
	}
}

func TestResolveClaudeBinarySkipsManagedAliasesAndHonorsPATHOrder(t *testing.T) {
	home := t.TempDir()
	managed := managedClaudeLauncher(home)
	if err := os.MkdirAll(filepath.Dir(managed), 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, managed)
	canonical := canonicalClaudeLauncher(home)
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(managed, canonical); err != nil {
		t.Fatal(err)
	}
	aliasDir := filepath.Join(home, "alias-bin")
	firstRealDir := filepath.Join(home, "first-real-bin")
	secondRealDir := filepath.Join(home, "second-real-bin")
	for _, directory := range []string{aliasDir, firstRealDir, secondRealDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(aliasDir, "claude")
	if err := os.Link(managed, alias); err != nil {
		t.Fatal(err)
	}
	firstReal := filepath.Join(firstRealDir, "claude")
	writeExecutable(t, firstReal)
	writeExecutable(t, filepath.Join(secondRealDir, "claude"))

	pathEnv := strings.Join(
		[]string{filepath.Dir(canonical), aliasDir, firstRealDir, secondRealDir},
		string(os.PathListSeparator),
	)
	resolved, err := ResolveClaudeBinary(home, alias, pathEnv)
	if err != nil || resolved != firstReal {
		t.Fatalf("resolution=%q err=%v, want first non-managed PATH candidate %q", resolved, err, firstReal)
	}
}

func TestResolveClaudeBinaryTreatsEmptyPATHComponentAsCurrentDirectory(t *testing.T) {
	home := t.TempDir()
	working := t.TempDir()
	writeExecutable(t, filepath.Join(working, "claude"))
	t.Chdir(working)

	resolved, err := ResolveClaudeBinary(home, "", string(os.PathListSeparator))
	want := filepath.Join(working, "claude")
	if err != nil || resolved != want {
		t.Fatalf("resolution=%q err=%v, want current-directory candidate %q", resolved, err, want)
	}
}

func TestResolveClaudeBinaryNamesAbsenceAndInspectionFailure(t *testing.T) {
	home := t.TempDir()
	if resolved, err := ResolveClaudeBinary(
		home,
		"",
		filepath.Join(home, "missing"),
	); !errors.Is(err, ErrClaudeBinaryNotFound) ||
		resolved != "" {
		t.Fatalf("absence resolution=%q err=%v, want ErrClaudeBinaryNotFound", resolved, err)
	}

	brokenHome := t.TempDir()
	versions := claudeVersionsDir(brokenHome)
	if err := os.MkdirAll(filepath.Dir(versions), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versions, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveClaudeBinary(brokenHome, "", ""); err == nil ||
		!strings.Contains(err.Error(), "inspect Claude versions") {
		t.Fatalf("inspection error=%v, want wrapped context", err)
	}
}

func TestInspectClaudeLauncherRejectsBrokenManagedTarget(t *testing.T) {
	home := t.TempDir()
	canonical := canonicalClaudeLauncher(home)
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(managedClaudeLauncher(home), canonical); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectClaudeLauncher(
		home,
	); err == nil ||
		!strings.Contains(err.Error(), "inspect managed Claude launcher") {
		t.Fatalf("InspectClaudeLauncher broken target error=%v", err)
	}
}

func TestClaudeAbsentIdentifiesOnlyPfmsLauncherAtExit127(t *testing.T) {
	home := t.TempDir()
	canonical := canonicalClaudeLauncher(home)
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(managedClaudeLauncher(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedClaudeLauncher(home), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(managedClaudeLauncher(home), canonical); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(home, "elsewhere", "claude")
	if err := os.MkdirAll(filepath.Dir(elsewhere), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(elsewhere, []byte("#!/bin/sh\nexit 127\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	if !ClaudeAbsent(home, canonical, 127) {
		t.Fatal("pfm's own launcher at exit 127 was not recognised as absent")
	}
	if ClaudeAbsent(home, canonical, 1) {
		t.Fatal("pfm's own launcher at exit 1 was wrongly treated as absent")
	}
	if ClaudeAbsent(home, elsewhere, 127) {
		t.Fatal("a non-pfm claude at exit 127 was wrongly treated as absent")
	}
}
