package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestReloadCommandTmuxRecordsUnderTheTmuxComponent: cmd/pfm's direct tmux
// calls terminate through the observed command. Against a socket nothing
// serves, tmux answers non-zero (or is absent) — either way exactly one
// comp=tmux record names the subcommand; no live server is touched.
func TestReloadCommandTmuxRecordsUnderTheTmuxComponent(t *testing.T) {
	ctx, recorder := obs.Test(t)
	socket := filepath.Join(t.TempDir(), "no-server")
	if err := (reloadCommandTmux{}).command(ctx, socket, "kill-server").Run(); err == nil {
		t.Fatal("kill-server on a socket nothing serves succeeded")
	}
	records := recorder.Records()
	if len(records) != 1 || records[0].Message != "tmux.exec" {
		t.Fatalf("want one tmux.exec record: %s", recorder.Raw())
	}
	if comp, _ := records[0].Field(obs.FieldComp); comp != "tmux" {
		t.Fatalf("comp = %v, want tmux", comp)
	}
	if subcmd, _ := records[0].Field("subcmd"); subcmd != "kill-server" {
		t.Fatalf("subcmd = %v", subcmd)
	}
	if _, found := records[0].Field(obs.FieldErr); !found {
		t.Fatalf("a failed invocation carries no err: %v", records[0].Fields)
	}
	_ = context.Background
}

// TestReloadCommandTmuxSetRemainAgainstNoServer: SetRemain's on/off branches
// both run their tmux invocation through the same command() door — against a
// socket nothing serves, both forms fail the same way the caller expects.
func TestReloadCommandTmuxSetRemainAgainstNoServer(t *testing.T) {
	ctx := context.Background()
	socket := filepath.Join(t.TempDir(), "no-server")
	tmux := reloadCommandTmux{}
	if err := tmux.SetRemain(ctx, socket, "%0", true); err == nil {
		t.Fatal("SetRemain(on) against a socket nothing serves succeeded")
	}
	if err := tmux.SetRemain(ctx, socket, "%0", false); err == nil {
		t.Fatal("SetRemain(off) against a socket nothing serves succeeded")
	}
}

// TestReloadRespawnLaunchesACommandLongerThanATmuxMessage: respawn-pane
// carries its command inside one tmux client message, capped near 16 KiB, so
// a Codex seat's reload — its launch line holds the composed fleet prompt —
// could not come back. The long command launches through a one-shot script
// that execs the target and removes itself.
func TestReloadRespawnLaunchesACommandLongerThanATmuxMessage(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := testjail.ShortRoot(t)
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	t.Setenv(paths.EnvTmuxConf, "/dev/null")
	const socket = "cx-1800000013-1-1"
	ctx := context.Background()
	if err := (spawn.TmuxSpawner{TmuxDir: tmuxDir}).NewSession(ctx, spawn.SessionSpec{
		Socket: socket, Session: socket, Window: "Codex", CWD: root, Width: 180, Height: 45, Run: "sleep 120",
	}); err != nil {
		t.Fatalf("create chat server: %v", err)
	}
	socketPath := filepath.Join(tmuxDir, socket)
	query := func(format string) string {
		command := exec.Command("tmux", "-S", socketPath, "display-message", "-p", "-t", socket+":", format)
		command.Env = append(os.Environ(), "TMUX=")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux display-message %s: %v: %s", format, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", socketPath, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		if output, err := kill.CombinedOutput(); err != nil {
			t.Logf("kill test server: %v: %s", err, output)
		}
	})
	marker := filepath.Join(root, "respawned")
	command := "PFM_LAUNCH_MARK='" + marker + "' sh -c 'touch \"$PFM_LAUNCH_MARK\"; exec sleep 120' pfm-launch '" +
		strings.Repeat("x", 64<<10) + "'"
	respawner := reloadCommandTmux{launchDir: tmuxDir}
	if err := respawner.Respawn(ctx, socketPath, query("#{pane_id}"), root, command); err != nil {
		t.Fatalf("respawn with a %d-byte command: %v", len(command), err)
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatalf("resolve sleep: %v", err)
	}
	want, err := filepath.EvalSymlinks(sleep)
	if err != nil {
		t.Fatalf("resolve sleep symlinks: %v", err)
	}
	var detail string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		entries, err := os.ReadDir(tmuxDir)
		if err != nil {
			t.Fatalf("read socket directory: %v", err)
		}
		pid := query("#{pane_pid}")
		exe, exeErr := os.Readlink("/proc/" + pid + "/exe")
		_, markErr := os.Stat(marker)
		detail = "entries=" + strconv.Itoa(len(entries)) + " pane " + pid + " exe=" + exe
		if markErr == nil && exeErr == nil && exe == want && len(entries) == 1 {
			return
		}
	}
	t.Fatalf("respawned pane never ran the target from a consumed script (want exe %s): %s", want, detail)
}
