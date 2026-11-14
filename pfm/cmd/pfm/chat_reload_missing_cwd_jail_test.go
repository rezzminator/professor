//go:build jail

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A reload whose recorded directory is gone opens in the fleet home and says so.
func TestChatReloadWorkerMissingCWDOpensInFleetHomeWithNotice(t *testing.T) {
	testjail.TmuxServer(t)
	root := jailTest(t)
	for _, tool := range []string{"tmux", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	captured := filepath.Join(t.TempDir(), "captured.txt")
	fixtureClaude := filepath.Join(t.TempDir(), "claude-fixture.sh")
	script := "#!/bin/sh\npwd >> '" + captured + "'\nexit 0\n"
	if err := testjail.WriteExecutable(fixtureClaude, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	goneCWD := filepath.Join(t.TempDir(), "gone")
	if err := os.MkdirAll(filepath.Join(root, "account-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := writeConfigFixture(t, root, `{
  "version": 1,
  "accounts": [
    {"id": 1, "configDir": "`+filepath.Join(root, "account-1")+`", "claude": {"binary": "`+fixtureClaude+`"}}
  ]
}`)
	promptScript := filepath.Join(t.TempDir(), "prompt.py")
	if err := testjail.WriteExecutable(promptScript, []byte(reloadPromptFixture), 0o700); err != nil {
		t.Fatal(err)
	}
	socket := probeReloadSocket(t, "gone")
	server := exec.Command(
		"tmux",
		"-S",
		socket,
		"-f",
		"/dev/null",
		"new-session",
		"-d",
		"-s",
		"probe",
		"python3",
		promptScript,
	)
	server.Env = append(server.Environ(), "TMUX=")
	if output, err := server.CombinedOutput(); err != nil {
		t.Fatalf("start probe socket: %v: %s", err, output)
	}
	cleanupProbeReloadSocket(t, socket)
	paneOutput, err := exec.Command("tmux", "-S", socket, "list-panes", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatalf("read probe pane: %v", err)
	}
	pane := strings.TrimSpace(string(paneOutput))
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(resolved.SIDDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(resolved.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(t.TempDir(), "44444444-4444-4444-8444-444444444444.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"cwd":"`+goneCWD+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	crumb := filepath.Join(resolved.SIDDir, filepath.Base(socket)+"."+pane)
	if err := os.WriteFile(crumb, []byte(transcript+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_RELOAD_DELAY_MS", "0")
	t.Setenv("PFM_RELOAD_POLL_MS", "20")
	t.Setenv("PFM_RELOAD_EXIT_TRIES", "50")
	t.Setenv("PFM_RELOAD_IDLE_TRIES", "500")
	runtime, err := pfmconfig.LoadRuntime(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runChatReloadWorkerWithRuntime(
		[]string{"--sock", socket, "--new", "--account", "1"}, &stdout, &stderr, runtime, nil,
	)
	if code != 0 {
		t.Fatalf("reload rc=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	want := "pfm chat reload: opened in " + resolved.Home + ": its own directory " + goneCWD + " is gone"
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr=%q missing notice %q", stderr.String(), want)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, readErr := os.ReadFile(captured)
		if readErr == nil && len(data) > 0 {
			if got := strings.TrimSpace(string(data)); got != resolved.Home {
				t.Fatalf("reload opened in %q, want the fleet home %q", got, resolved.Home)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture claude never ran: err=%v stderr=%q", readErr, stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
