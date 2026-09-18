package tmux

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
)

// fakeTmux writes a shell script that plays tmux: it prints its arguments
// and exits with the code the test names.
func fakeTmux(t *testing.T, exit int) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tmux-fake")
	script := "#!/bin/sh\nprintf '%s ' \"$@\"\nexit " + strconv.Itoa(exit) + "\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

func oneRecord(t *testing.T, recorder *obs.Recorder) obs.Record {
	t.Helper()
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want exactly one per invocation: %s", len(records), recorder.Raw())
	}
	return records[0]
}

// TestExecOutputRecordsSubcommandTargetExitOnce: Output completes the
// command and writes ONE comp=tmux record — subcmd, target, exit, dur_ms —
// while the arguments (a send-keys body is a prompt) never reach the file.
func TestExecOutputRecordsSubcommandTargetExitOnce(t *testing.T) {
	ctx, recorder := obs.Test(t)
	output, err := Exec(ctx, fakeTmux(t, 0), "/sockets/cc-1", "send-keys", "-t", "%7", "PLANTED prompt body").Output()
	if err != nil || !strings.Contains(string(output), "PLANTED") {
		t.Fatalf("Output = %q, %v — the wrapper changed the command's result", output, err)
	}
	record := oneRecord(t, recorder)
	if record.Message != "tmux.exec" || record.Level != slog.LevelInfo.String() {
		t.Fatalf("record = %s at %s, want tmux.exec at INFO", record.Message, record.Level)
	}
	for key, want := range map[string]any{obs.FieldComp: "tmux", "subcmd": "send-keys", "target": "%7", obs.FieldExit: float64(0)} {
		if got, found := record.Field(key); !found || got != want {
			t.Fatalf("%s = %v (found %t), want %v", key, got, found, want)
		}
	}
	if _, found := record.Field(obs.FieldDur); !found {
		t.Fatalf("no dur_ms: %v", record.Fields)
	}
	if strings.Contains(recorder.Raw(), "PLANTED") {
		t.Fatalf("a send-keys body reached the file: %s", recorder.Raw())
	}
}

// TestExecRunAndCombinedOutputCarryTheExitCode: a tmux that exits non-zero
// is the command answering — INFO with the exit code, err carried, the
// caller's error untouched.
func TestExecRunAndCombinedOutputCarryTheExitCode(t *testing.T) {
	ctx, recorder := obs.Test(t)
	binary := fakeTmux(t, 3)
	if err := Exec(ctx, binary, "/sockets/cc-1", "kill-server").Run(); err == nil {
		t.Fatal("Run swallowed the exit status")
	}
	if _, err := Exec(ctx, binary, "/sockets/cc-1", "list-panes", "-a").CombinedOutput(); err == nil {
		t.Fatal("CombinedOutput swallowed the exit status")
	}
	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want one per invocation: %s", len(records), recorder.Raw())
	}
	for _, record := range records {
		if exit, _ := record.Field(obs.FieldExit); exit != float64(3) {
			t.Fatalf("exit = %v, want 3", exit)
		}
		if _, found := record.Field(obs.FieldErr); !found {
			t.Fatalf("no err on a failed invocation: %v", record.Fields)
		}
	}
	if subcmd, _ := records[0].Field("subcmd"); subcmd != "kill-server" {
		t.Fatalf("subcmd = %v", subcmd)
	}
	if _, found := records[0].Field("target"); found {
		t.Fatalf("kill-server has no -t target, yet one was recorded: %v", records[0].Fields)
	}
}

// TestExecStartRecordsAtWait: Start alone writes nothing (the command is
// still running); Wait writes the one record. A Start that cannot run is an
// ERROR record with exit -1.
func TestExecStartRecordsAtWait(t *testing.T) {
	ctx, recorder := obs.Test(t)
	command := Exec(ctx, fakeTmux(t, 0), "/sockets/cc-1", "wait-for", "pfm-done")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if len(recorder.Records()) != 0 {
		t.Fatalf("Start wrote a record before completion: %s", recorder.Raw())
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	record := oneRecord(t, recorder)
	if subcmd, _ := record.Field("subcmd"); subcmd != "wait-for" {
		t.Fatalf("subcmd = %v", subcmd)
	}
	missing := Exec(ctx, filepath.Join(t.TempDir(), "no-tmux"), "/sockets/cc-1", "list-panes")
	if err := missing.Start(); err == nil {
		t.Fatal("a missing binary started")
	}
	records := recorder.Records()
	if len(records) != 2 || records[1].Level != slog.LevelError.String() {
		t.Fatalf("a failed start is one ERROR record: %s", recorder.Raw())
	}
	if exit, _ := records[1].Field(obs.FieldExit); exit != float64(-1) {
		t.Fatalf("failed start exit = %v, want -1", exit)
	}
}

// TestExecKeepsCommandsContract: the terminal is Command plus completion —
// same binary, same -S socket argv, same cleared TMUX, and the embedded
// *exec.Cmd still takes Stdin and exposes Process for the façades that
// set or kill them.
func TestExecKeepsCommandsContract(t *testing.T) {
	t.Setenv("TMUX", "/tmp/caller-server,1,0")
	binary := fakeTmux(t, 0)
	command := Exec(context.Background(), binary, "/sockets/cc-1", "load-buffer", "-b", "buf", "-")
	if command.Path != binary || command.Args[1] != "-S" || command.Args[2] != "/sockets/cc-1" {
		t.Fatalf("argv = %q", command.Args)
	}
	command.Stdin = strings.NewReader("body")
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	if command.Process == nil {
		t.Fatal("the embedded exec.Cmd lost its Process")
	}
}

// TestObserveRecordsALauncherAssembledCommand: a new-session run under
// another launcher (spawn's systemd scope) still writes the tmux record,
// shaped from the tmux arguments, not the launcher's.
func TestObserveRecordsALauncherAssembledCommand(t *testing.T) {
	ctx, recorder := obs.Test(t)
	launcher := exec.CommandContext(
		ctx,
		fakeTmux(t, 0),
		"--scope",
		"--",
		"tmux",
		"-S",
		"/sockets/cc-1",
		"new-session",
		"-d",
	)
	if _, err := Observe(ctx, launcher, "new-session", "-d").CombinedOutput(); err != nil {
		t.Fatal(err)
	}
	record := oneRecord(t, recorder)
	if subcmd, _ := record.Field("subcmd"); subcmd != "new-session" {
		t.Fatalf("subcmd = %v, want new-session", subcmd)
	}
}
