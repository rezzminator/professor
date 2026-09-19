package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hostops/pfm/internal/deps"
)

// `pfm chat reload --sock ox-…` used to print "reload scheduled in place" and
// exit 0, then fail detached where nobody was reading: the worker has no way
// to identify an OpenCode seat (no session env, no crumb). A refusal the
// caller can see beats a success it cannot. The worker seam must never be
// touched, and stdout must stay empty.
func TestChatReloadRefusesAnOpenCodeSocketUpFront(t *testing.T) {
	root := jailTest(t)
	configPath := writeConfigFixture(t, root, `{
  "version": 1,
  "accounts": [{"id": 1, "configDir": "`+filepath.Join(root, "account-1")+`"}]
}`)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	dir := filepath.Join(os.TempDir(), "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "ox-probe-pfm-reload-"+strconv.Itoa(os.Getpid()))
	server := exec.Command(
		"tmux", "-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", "probe", "sleep 120",
	)
	server.Env = append(server.Environ(), "TMUX=")
	if output, err := server.CombinedOutput(); err != nil {
		t.Fatalf("start probe socket: %v: %s", err, output)
	}
	cleanupProbeReloadSocket(t, socket)

	old := startReloadWorker
	t.Cleanup(func() { startReloadWorker = old })
	started := 0
	startReloadWorker = func([]string, deps.StartOptions) error {
		started++
		return nil
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--config", configPath, "chat", "reload", "--sock", socket, "1"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("rc = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if started != 0 {
		t.Fatalf("the reload worker was started %d time(s) for an OpenCode socket", started)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty: nothing was scheduled", stdout.String())
	}
	want := "pfm chat reload: OpenCode chats cannot be reloaded yet (" + filepath.Base(socket) + ")"
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
	if _, err := os.Stat(filepath.Join(root, "sid", "reload-"+filepath.Base(socket)+".log")); err == nil {
		t.Fatal("a worker log was written for a reload that never ran")
	}
}
