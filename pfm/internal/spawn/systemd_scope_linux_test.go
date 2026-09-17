//go:build linux

package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatSpawnInsideUserServiceUsesTransientScope(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "systemd-run.log")
	writeExecutable(t, filepath.Join(bin, "systemd-run"), `#!/bin/sh
printf '%s\n' "$*" >> "$SYSTEMD_RUN_LOG"
[ "$1" = --user ] && [ "$2" = --collect ] && [ "$3" = --scope ] && [ "$4" = -- ] || exit 91
shift 4
exec "$@"
`)
	tmuxPath := filepath.Join(bin, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
case "$*" in
  *" new-session "*) [ -z "$INVOCATION_ID" ] || exit 92 ;;
esac
exit 0
`)

	t.Setenv("PATH", bin)
	t.Setenv("INVOCATION_ID", "service-invocation")
	t.Setenv("SYSTEMD_RUN_LOG", logPath)
	tmux := TmuxSpawner{Binary: tmuxPath, TmuxDir: filepath.Join(root, "tmux")}
	if err := tmux.NewSession(context.Background(), SessionSpec{
		Socket: "cx-scope", Session: "cx-scope", Window: "Codex", CWD: root,
		Width: 180, Height: 45, Run: "sleep 120",
	}); err != nil {
		t.Fatalf("spawn under service: %v", err)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("systemd-run was not invoked: %v", err)
	}
	want := "--user --collect --scope -- " + tmuxPath + " -S "
	if !strings.HasPrefix(string(logged), want) || !strings.Contains(string(logged), " new-session -d ") {
		t.Fatalf("systemd-run argv = %q, want scoped tmux new-session", logged)
	}
}

func TestChatSpawnInsideUserServiceRefusesWithoutSystemdRun(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	tmuxPath := filepath.Join(bin, "tmux")
	writeExecutable(t, tmuxPath, "#!/bin/sh\nexit 0\n")

	t.Setenv("PATH", bin)
	t.Setenv("INVOCATION_ID", "service-invocation")
	err := (TmuxSpawner{Binary: tmuxPath, TmuxDir: filepath.Join(root, "tmux")}).NewSession(
		context.Background(),
		SessionSpec{
			Socket: "cx-mortal", Session: "cx-mortal", Window: "Codex", CWD: root,
			Width: 180, Height: 45, Run: "sleep 120",
		},
	)
	if err == nil || !strings.Contains(err.Error(), "systemd-run") {
		t.Fatalf("spawn without systemd-run error = %v, want loud refusal", err)
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}
