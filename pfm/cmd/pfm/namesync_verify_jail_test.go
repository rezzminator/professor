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

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/testjail"
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
