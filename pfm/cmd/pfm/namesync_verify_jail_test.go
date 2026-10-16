package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

func TestNameSyncDefaultsToPreview(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := testjail.Fleet(t)
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	t.Setenv(paths.EnvTmuxDir, tmuxDir)
	resolved := jailPaths(t)
	const (
		socket      = "cc-1800000099-42-1"
		original    = "ORIGINAL"
		targetLabel = "PREVIEW_TARGET"
	)
	socketPath := filepath.Join(tmuxDir, socket)
	start := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", socket, "-n", original,
		"printf '🥇 acct  🔖 "+targetLabel+" │ 42%%\\n'; sleep 120",
	)
	start.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start preview server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", socketPath, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		_ = kill.Run()
	})

	runtime := commandRuntime{
		Paths: resolved,
		Config: pfmconfig.Defaults(
			resolved.Home,
			resolved.Roots[pfmengine.Claude],
			resolved.FirstRoot(pfmengine.Codex),
		),
	}
	var stdout, stderr bytes.Buffer
	if code := runNameSync(nil, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("runNameSync() code=%d stderr=%q", code, stderr.String())
	}
	if got := readProbe(t, socketPath, "display-message", "-p", "#{window_name}"); got != original {
		t.Fatalf("window name = %q, want unchanged %q in the default preview", got, original)
	}
	if !strings.Contains(stdout.String(), "would rename "+socket) ||
		!strings.Contains(stdout.String(), original+" -> "+targetLabel) {
		t.Fatalf("stdout=%q, want the planned rename", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runNameSync([]string{"--dry-run"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("runNameSync(--dry-run) code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "dry run is the default; --apply performs the renames\n") {
		t.Fatalf("stderr=%q, want the deprecated alias note", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runNameSync([]string{"--apply"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("runNameSync(--apply) code=%d stderr=%q", code, stderr.String())
	}
	if got := readProbe(t, socketPath, "display-message", "-p", "#{window_name}"); got != targetLabel {
		t.Fatalf("window name = %q, want applied %q", got, targetLabel)
	}
}

// name-sync reports what it ACHIEVED, not what it attempted. The fixture puts
// one window in each state on a probe server: one whose name matches what was
// asked for, one a second writer took back, and one whose server is gone.
func TestNameSyncCountsOnlyVerifiedWindowsAsConverged(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	base := filepath.Join(os.TempDir(), "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "probe-pfm-verify-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove probe jail: %v", err)
		}
	})

	const socket = "probe-verify"
	socketPath := filepath.Join(root, socket)
	start := exec.Command(
		"tmux", "-S", socketPath, "-f", "/dev/null",
		"new-session", "-d", "-s", socket, "-n", "CONVERGED", "sleep 120",
	)
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start probe server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socketPath, "kill-server").Run()
	})
	if output, err := exec.Command(
		"tmux", "-S", socketPath, "new-window", "-d", "-n", "TAKEN-BACK", "sleep 120",
	).CombinedOutput(); err != nil {
		t.Fatalf("create second window: %v: %s", err, output)
	}
	windowIDs := strings.Fields(readProbe(t, socketPath, "list-windows", "-F", "#{window_id}"))
	if len(windowIDs) != 2 {
		t.Fatalf("probe server has windows %v, want two", windowIDs)
	}

	runtime := commandRuntime{Paths: paths.Values{TmuxDir: root}}
	var stderr bytes.Buffer
	converged, unverified := verifyRenames(context.Background(), runtime, []gather.WindowRename{
		{Socket: socket, WindowID: windowIDs[0], TargetName: "CONVERGED"},
		{Socket: socket, WindowID: windowIDs[1], TargetName: "WANTED"},
		{Socket: "probe-verify-gone", WindowID: "@0", TargetName: "WANTED"},
	}, &stderr)

	if converged != 1 {
		t.Fatalf("converged = %d, want 1 — only the window whose name reads back counts", converged)
	}
	if unverified != 2 {
		t.Fatalf("unverified = %d, want 2", unverified)
	}
	report := stderr.String()
	if !strings.Contains(report, `wanted "WANTED", reads "TAKEN-BACK" after rename`) {
		t.Fatalf("report does not name the value read back:\n%s", report)
	}
	if !strings.Contains(report, "could not be read back after rename") {
		t.Fatalf("report does not name the unreadable window:\n%s", report)
	}
}

func readProbe(t *testing.T, socketPath string, arguments ...string) string {
	t.Helper()
	command := exec.Command("tmux", append([]string{"-S", socketPath}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

// namesyncThemeState is a state.json a `pfm theme on` left: enabled, one
// derived dark theme applied at generation 1.
const namesyncThemeState = `{"enabled":true,"appearance":"dark","generation":1,"applied":{"value":"custom:professor-gold",` +
	`"title":"Professor Gold","side":"dark","primary":{"background":"#1a1b26","foreground":"#c0caf5","cursor":"#c0caf5",` +
	`"selectionBackground":"#283457","selectionForeground":"#c0caf5","surface":"#1f2335","overlay":"#292e42",` +
	`"muted":"#565f89","accent":"#7aa2f7","orange":"#ec937b","ansi":["#1a1b26","#f7768e","#9ece6a","#e0af68","#7aa2f7",` +
	`"#bb9af7","#7dcfff","#c0caf5","#565f89","#f7768e","#9ece6a","#e0af68","#7aa2f7","#bb9af7","#7dcfff","#c0caf5"],` +
	`"state":{"blocked":"#bb9af7","working":"#7aa2f7","done":"#9ece6a","idle":"#7b84af"}}}}`

// name-sync --apply carries the theme to every live chat server through the
// theme's own door: nothing is said or touched while `pfm theme` has never
// been set, and with the sync on each live server is themed and counted.
func TestNameSyncThemesLiveServersOnlyWhileTheSyncIsOn(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := testjail.Fleet(t)
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	t.Setenv(paths.EnvTmuxDir, tmuxDir)
	resolved := jailPaths(t)
	const socket = "cc-1800000098-42-1"
	start := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null", "new-session", "-d", "-s", socket, "-n", "W", "sleep 120",
	)
	start.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", filepath.Join(tmuxDir, socket), "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		_ = kill.Run()
	})
	runtime := commandRuntime{
		Paths: resolved,
		Config: pfmconfig.Defaults(
			resolved.Home,
			resolved.Roots[pfmengine.Claude],
			resolved.FirstRoot(pfmengine.Codex),
		),
	}

	var off, offErr bytes.Buffer
	runNameSync([]string{"--apply"}, &off, &offErr, runtime)
	if strings.Contains(off.String()+offErr.String(), "tmux theme") {
		t.Fatalf(
			"name-sync mentioned the theme with the sync never set:\nstdout=%q\nstderr=%q",
			off.String(),
			offErr.String(),
		)
	}

	stateDir := themesync.DefaultStateDir(resolved.Home)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(stateDir, themesync.StateFileName),
		[]byte(namesyncThemeState),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	var on, onErr bytes.Buffer
	if code := runNameSync([]string{"--apply"}, &on, &onErr, runtime); code != 0 ||
		!strings.Contains(on.String(), "tmux theme applied: 1") {
		t.Fatalf("with the sync on, name-sync must theme the live server: code=%d\nstdout=%q\nstderr=%q",
			code, on.String(), onErr.String())
	}
	if got := readProbe(
		t,
		filepath.Join(tmuxDir, socket),
		"show",
		"-gv",
		"@pfm-theme",
	); got != "custom:professor-gold" {
		t.Fatalf("server @pfm-theme = %q, want custom:professor-gold", got)
	}
}
