//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/installer"
	pfmpaths "github.com/rezzminator/professor/pfm/internal/paths"
)

// schedulerRowWarnings reports how many warnings doctor's service-manager row
// contributes on THIS host. The jail's scheduler fixture answers every probe
// but never enables the pfm-mcp unit, so the row warns wherever a user
// service manager answers at all (Linux systemd here) and stays silent where
// the manager is absent or reports the unit healthy — printServiceManagerDoctor
// renders those states apart, and only two of them are warnings.
//
// The row is a property of the HOST, not of the install, so the warning tally
// in requireSkippedHarvestDoctor adds it instead of hard-coding a number that
// would be right on one e2e platform and wrong on the other. Every OTHER
// warning still has to be zero: the count remains the check that a new
// unexpected doctor warning cannot slip through the fresh-install e2e.
func schedulerRowWarnings(output string) int {
	if strings.Contains(output, "doctor: service-manager=") &&
		(strings.Contains(output, "could_not_ask") || strings.Contains(output, "— start with: ")) {
		return 1
	}
	return 0
}

// The target-shape home has no legitimate host-check WARN rows: its store
// and account entries are classified, and no legacy files are staged.
func (h *e2eHarness) hostCheckRowWarnings(output string) int {
	h.t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "host-check: WARN ") {
			h.t.Fatalf("unexpected host-check warning on fresh home: %s", line)
		}
	}
	return 0
}

// claudePluginRowWarnings counts doctor's store-wide plugin gap rows.
func claudePluginRowWarnings(output string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "doctor: claude_plugins ") &&
			strings.HasSuffix(line, " — run pfm install --yes") {
			count++
		}
	}
	return count
}

func (h *e2eHarness) requireSkippedHarvestDoctor(result commandResult) {
	h.t.Helper()
	output := result.stdout + result.stderr
	if result.err == nil {
		h.t.Fatalf("doctor after --skip-harvest succeeded, want named unprovisioned dependencies; output=%q", output)
	}
	for _, want := range []string{
		"doctor: dep uv path= broken",
		"doctor: dep harvestpy path= broken",
		"doctor: harvestpy skipped",
		"doctor: pre-push gate=armed core.hooksPath=.githooks",
		"doctor: harness-prompt: matches baseline",
		"doctor: service-manager=",
	} {
		if !strings.Contains(output, want) {
			h.t.Fatalf(
				"doctor after --skip-harvest omitted %q; stdout=%q stderr=%q",
				want,
				result.stdout,
				result.stderr,
			)
		}
	}
	wantWarnings := fmt.Sprintf("doctor: warnings=%d", 2+schedulerRowWarnings(output)+h.hostCheckRowWarnings(output))
	if !strings.Contains("\n"+output, "\n"+wantWarnings+"\n") {
		h.t.Fatalf("doctor omitted exact tally %q: %s", wantWarnings, output)
	}
	for _, prefix := range []string{"host-check: BLOCK ", "store: ", "account: ", "account-link: "} {
		for _, line := range strings.Split(output, "\n") {
			if strings.HasPrefix(line, prefix) {
				h.t.Fatalf("doctor found broken install row: %s", line)
			}
		}
	}
	if gaps := claudePluginRowWarnings(output); gaps != 0 {
		h.t.Fatalf("doctor found %d Claude plugin gaps after install: %s", gaps, output)
	}
	// M2 (issue #24 finding 1): the unprovisioned harvestpy sidecar deps
	// (uv, harvestpy) above are warnings, not failures — the fleet engine
	// runs without them, and `--skip-harvest` is pfm's own decision not to
	// provision them. `doctor: failures=` must never appear here.
	if strings.Contains(output, "doctor: failures=") {
		h.t.Fatalf(
			"doctor after --skip-harvest printed a failures= line for warnings-only rows; stdout=%q stderr=%q",
			result.stdout,
			result.stderr,
		)
	}
}

func (h *e2eHarness) assertInstalled(home string) {
	h.t.Helper()
	managed := filepath.Join(home, e2eManagedRoot)
	if info, err := os.Stat(managed); err != nil || !info.IsDir() {
		h.t.Fatalf("install surface failed; differing paths: %s; status: %v", e2eManagedRoot, err)
	}
	for _, relative := range managedAssets {
		if _, err := os.Stat(filepath.Join(managed, relative)); err != nil {
			h.t.Fatalf(
				"install surface failed; differing paths: %s; status: %v",
				filepath.Join(e2eManagedRoot, relative),
				err,
			)
		}
	}
	for _, relative := range []string{"source-repo", "binary-ownership.json"} {
		if _, err := os.Stat(filepath.Join(managed, relative)); err != nil {
			h.t.Fatalf(
				"install surface failed; differing paths: %s; status: %v",
				filepath.Join(e2eManagedRoot, relative),
				err,
			)
		}
	}
	canonicalClaude := filepath.Join(home, e2eCanonicalClaude)
	managedClaude := filepath.Join(managed, "bin", "claude")
	info, err := os.Lstat(canonicalClaude)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		h.t.Fatalf("install surface failed; differing paths: %s launcher link; status: %v", e2eCanonicalClaude, err)
	}
	target, err := os.Readlink(canonicalClaude)
	if err != nil || filepath.Clean(target) != filepath.Clean(managedClaude) {
		h.t.Fatalf("install surface failed; differing paths: %s target=%q; status: %v", e2eCanonicalClaude, target, err)
	}
	if _, err := os.Stat(filepath.Join(managed, "launcher.state")); err != nil {
		h.t.Fatalf("install surface failed; differing paths: launcher.state; status: %v", err)
	}
	h.readJSON(filepath.Join(managed, "binary-ownership.json"))
	if runtime.GOOS == "linux" {
		for _, relative := range []string{
			"systemd/pfm-name-sync.path", "systemd/pfm-name-sync.service", "systemd/pfm-name-sync.timer",
			"systemd/pfm-reminder.service", "systemd/pfm-reminder.timer",
		} {
			if _, err := os.Stat(filepath.Join(managed, relative)); err != nil {
				h.t.Fatalf(
					"install surface failed; differing paths: %s; status: %v",
					filepath.Join(e2eManagedRoot, relative),
					err,
				)
			}
		}
	}
	store := installer.ClaudeStore(home)
	if gaps, err := installer.ClaudePluginGaps(filepath.Join(store, "settings.json")); err != nil || len(gaps) != 0 {
		h.t.Fatalf("store plugin settings gaps=%v: %v", gaps, err)
	}
	if missing, err := installer.ClaudePluginsNotInstalled(store); err != nil || len(missing) != 0 {
		h.t.Fatalf("store plugins missing=%v: %v", missing, err)
	}
	for _, entry := range installer.StoreEntries {
		target := filepath.Join(store, entry.Name)
		info, err := os.Stat(target)
		if err != nil || info.IsDir() != entry.Dir {
			h.t.Fatalf("store entry %s shape: %v", target, err)
		}
		for _, relative := range managedSettings {
			path := filepath.Join(home, filepath.Dir(relative), entry.Name)
			link, err := os.Readlink(path)
			if err != nil || filepath.Clean(link) != filepath.Clean(target) {
				h.t.Fatalf("account link %s=%q, want %s: %v", path, link, target, err)
			}
		}
	}
	for _, relative := range managedSettings {
		account := filepath.Join(home, filepath.Dir(relative))
		info, err := os.Lstat(account)
		if err != nil || !info.IsDir() {
			h.t.Fatalf("account %s is not a real directory: %v", account, err)
		}
		h.readJSON(filepath.Join(home, relative))
	}
	h.assertCommandLinksInstalled(home)
	// The shim is static and sourced straight from the clone the marker names;
	// nothing is staged under the managed root.
	clone, err := pfmpaths.ReadSourceRepoMarker(home)
	if err != nil {
		h.t.Fatalf("install surface failed; differing paths: source-repo marker; status: %v", err)
	}
	shim := filepath.Join(clone, "pfm", "internal", "installer", "assets", "shim", "pfm.zsh")
	if result := runTool(home, "zsh", "-n", shim); result.err != nil {
		h.t.Fatalf("install surface failed; differing paths: shim/pfm.zsh syntax; status: %v", result.err)
	}
	if !hasSourceLine(filepath.Join(home, e2eZshrc), shim) {
		zshrc, readErr := os.ReadFile(filepath.Join(home, e2eZshrc))
		h.t.Fatalf("install surface failed; differing paths: .zshrc source line for %s; zshrc=%q status=%v",
			shim, zshrc, readErr)
	}
	h.assertTmuxConfig(home)
	codexHooksPath := filepath.Join(home, e2eCodexHooks)
	if raw, err := os.ReadFile(codexHooksPath); err == nil {
		var codex map[string]any
		if err := json.Unmarshal(raw, &codex); err != nil {
			h.t.Fatalf("install surface failed; differing paths: .codex/hooks.json parse; status: %v", err)
		}
		if containsJSONString(codex, filepath.Join(home, ".local", "bin", "pfm")+" internal clear-kill") {
			h.t.Fatal("install surface failed; differing paths: .codex/hooks.json retained retired clear-kill")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		h.t.Fatalf("install surface failed; differing paths: .codex/hooks.json; status: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, e2eSourceMarker)); err != nil {
		h.t.Fatalf("install surface failed; differing paths: %s; status: %v", e2eSourceMarker, err)
	}
	if _, err := os.Stat(filepath.Join(home, e2eCanonicalPFM)); err != nil {
		h.t.Fatalf("install surface failed; differing paths: %s; status: %v", e2eCanonicalPFM, err)
	}
	if runtime.GOOS == "linux" {
		for _, name := range []string{"pfm-name-sync.path", "pfm-name-sync.service", "pfm-name-sync.timer", "pfm-reminder.service", "pfm-reminder.timer"} {
			if _, err := os.Stat(filepath.Join(home, ".config", "systemd", "user", name)); err != nil {
				h.t.Fatalf("install surface failed; differing paths: systemd/%s; status: %v", name, err)
			}
		}
	} else if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "com.professor.pfm.name-sync.plist")); err != nil {
		h.t.Fatalf("install surface failed; differing paths: launchd name-sync; status: %v", err)
	} else if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "com.professor.pfm.reminder.plist")); err != nil {
		h.t.Fatalf("install surface failed; differing paths: launchd reminder; status: %v", err)
	}
}

func (h *e2eHarness) assertOldShapeInstallConverges(fresh surfaceSnapshot) {
	h.t.Helper()
	home := h.oldShapeHome(h.headBinary)
	before := h.refusalSnapshot(home)
	if !h.t.Run("refusal", func(t *testing.T) {
		result := h.pfm(home, "install", "--yes", "--skip-harvest")
		var exit *exec.ExitError
		if !errors.As(result.err, &exit) || exit.ExitCode() != 4 {
			t.Fatalf("old-shape install status=%v, want 4; output=%s%s", result.err, result.stdout, result.stderr)
		}
		var want []string
		store := installer.ClaudeStore(home)
		for _, relative := range managedSettings {
			account := filepath.Join(home, filepath.Dir(relative))
			projects, target := filepath.Join(account, "projects"), filepath.Join(store, "projects")
			settings := filepath.Join(account, "settings.json")
			// Each BLOCK row carries its fix: a rolled-back binary's doctor may have no host checks.
			want = append(
				want,
				"pfm install: BLOCK account-entry-real "+projects+" — projects is a real dir; it belongs in the store",
				"pfm install:   fix: "+unionFix(projects, target),
				"pfm install: BLOCK account-entry-real "+settings+" — settings.json is a real file; it belongs in the store",
				"pfm install:   fix: "+settingsFix(settings, filepath.Join(store, "settings.json")),
			)
		}
		want = append(want, "pfm install: 6 blocking — run pfm doctor for the fixes")
		if result.stdout != "" || result.stderr != strings.Join(want, "\n")+"\n" {
			t.Fatalf("old-shape refusal rows: stdout=%q stderr=%q, want %q", result.stdout, result.stderr, want)
		}
		differences, err := snapshotDifferences(before, h.refusalSnapshot(home))
		if err != nil || len(differences) != 0 {
			t.Fatalf("refusal changed home: %v; %v", differences, err)
		}
	}) {
		return
	}
	if !h.t.Run("doctor fixes", func(t *testing.T) {
		result := h.pfm(home, "doctor")
		var exit *exec.ExitError
		if !errors.As(result.err, &exit) || exit.ExitCode() != 3 {
			t.Fatalf("old-shape doctor status=%v, want 3; output=%s%s", result.err, result.stdout, result.stderr)
		}
		output := result.stdout + result.stderr
		store := installer.ClaudeStore(home)
		for _, relative := range managedSettings {
			account := filepath.Join(home, filepath.Dir(relative))
			projects, target := filepath.Join(account, "projects"), filepath.Join(store, "projects")
			for _, fix := range []string{
				unionFix(projects, target),
				settingsFix(filepath.Join(account, "settings.json"), filepath.Join(store, "settings.json")),
			} {
				if !strings.Contains(output, "host-check:   fix: "+fix+"\n") {
					t.Errorf("doctor omitted fix %q: %s", fix, output)
				}
			}
		}
		if got := strings.Count(output, "host-check: BLOCK account-entry-real "); got != 6 {
			t.Errorf("doctor account-entry-real rows=%d, want 6: %s", got, output)
		}
	}) {
		return
	}
	h.t.Run("install after fixes", func(t *testing.T) {
		fixed := *h
		fixed.t = t
		fixed.applyOldShapeFixes(home)
		result := fixed.pfm(home, "install", "--yes", "--skip-harvest")
		fixed.requireSuccess("install after doctor fixes", result)
		fixed.requireHarvestGate("install after doctor fixes", result)
		fixed.assertInstalled(home)
		converged, err := fixed.snapshot(home)
		if err != nil {
			t.Fatal(err)
		}
		differences, err := snapshotDifferences(fresh, converged)
		if err != nil || len(differences) != 0 {
			t.Fatalf("fixed install convergence failed: %v; %v", differences, err)
		}
	})
}

func (h *e2eHarness) applyOldShapeFixes(home string) {
	h.t.Helper()
	store := installer.ClaudeStore(home)
	target := filepath.Join(store, "projects")
	settingsPath := filepath.Join(store, "settings.json")
	settings := h.readJSON(settingsPath)
	for _, relative := range managedSettings {
		account := filepath.Join(home, filepath.Dir(relative))
		projects := filepath.Join(account, "projects")
		h.requireSuccess("union projects into store", h.tool(home, "cp", "-an", projects+"/.", target+"/"))
		comparison := h.tool(home, "diff", "-rq", projects, target)
		var exit *exec.ExitError
		if comparison.err != nil && (!errors.As(comparison.err, &exit) || exit.ExitCode() != 1) {
			h.t.Fatalf("compare merged projects: %v: %s%s", comparison.err, comparison.stdout, comparison.stderr)
		}
		for _, line := range strings.Split(strings.TrimSpace(comparison.stdout), "\n") {
			if line != "" && !strings.HasPrefix(line, "Only in "+target) {
				h.t.Fatalf("merged project differs: %s", line)
			}
		}
		if err := os.RemoveAll(projects); err != nil {
			h.t.Fatal(err)
		}
		path := filepath.Join(home, relative)
		for key, value := range h.readJSON(path) {
			settings[key] = value
		}
		h.writeJSON(settingsPath, settings)
		if err := os.Remove(path); err != nil {
			h.t.Fatal(err)
		}
	}
}

// unionFix and settingsFix are account-entry-real's printed fixes for a real
// projects dir and settings file, as the host check renders them.
func unionFix(path, target string) string {
	return "mkdir -p " + target + " && cp -an " + path + "/. " + target + "/ && ! diff -rq " + path + " " + target +
		" 2>&1 | grep -v '^Only in " + target + "' && rm -r " + path +
		"  # union into the store; stops while a file differs"
}

func settingsFix(path, target string) string {
	return "jq -e -s '.[0] as $s | .[1] | to_entries | " +
		"all(.key as $k | ($s | has($k) | not) or $s[$k] == .value)' " +
		target + " " + path + " > /dev/null && jq -s '.[0] * .[1]' " + target + " " + path + " > " + target +
		".new && mv " + target + ".new " + target + " && rm " + path +
		"  # adds the keys the store lacks; stops while a key differs"
}
