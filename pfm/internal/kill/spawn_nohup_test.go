package kill

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// TestCommandSpawnerUsesNohupWhenSetsidIsAbsent pins the branch the fix
// exists for — every macOS kill, since Darwin ships no setsid. Three facts
// distinguish this from the setsid path in TestCommandSpawnerUsesSetsidSelfReexec:
// the finisher argv must carry no "-f" (nohup takes no such flag); Spawn
// must Start()+Release() rather than Run(), so it returns before the
// finisher exits; and a parent context cancelled right after Spawn returns
// must not kill the finisher — under nohup the launched process IS the
// finisher, and killing it defeats the whole detach.
func TestCommandSpawnerUsesNohupWhenSetsidIsAbsent(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not installed")
	}
	root := t.TempDir()
	argvPath := filepath.Join(root, "argv")
	donePath := filepath.Join(root, "done")
	nohupPath := filepath.Join(root, "nohup")
	writeTestFile(
		t,
		nohupPath,
		"#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PFM_SPAWN_ARGV\"\nsleep 1\ntouch \"$PFM_SPAWN_DONE\"\n",
	)
	if err := os.Chmod(nohupPath, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_SPAWN_ARGV", argvPath)
	t.Setenv("PFM_SPAWN_DONE", donePath)
	spawner := CommandSpawner{
		Executable: "/jail/pfm",
		Setsid:     filepath.Join(root, "no-such-setsid-binary"),
		Nohup:      nohupPath,
		ConfigPath: "/jail/config/pfm.json",
	}
	args := ExitArgs{
		Engine:     pfmengine.Claude,
		ID:         "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		DataPath:   "/jail/transcript.jsonl",
		SocketPath: "/jail/tmux/cc-2-1-1",
		SocketName: "cc-2-1-1",
		PaneID:     "%4",
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := time.Now()
	if err := spawner.Spawn(ctx, args); err != nil {
		cancel()
		t.Fatalf("Spawn returned an error on the nohup branch: %v", err)
	}
	elapsed := time.Since(started)
	// Cancel immediately: the nohup branch must not bind the launched
	// process to ctx, or this kills the finisher before its 1s sleep ends.
	cancel()
	if elapsed >= 900*time.Millisecond {
		t.Fatalf("Spawn took %v — it must Start()+Release() the nohup helper, not wait on it", elapsed)
	}
	// The fixture's own "sleep 1" already spends a second of this wait, so a
	// flat 3s budget leaves only ~2s of margin for the finisher to actually
	// be forked and scheduled — under a loaded suite running every package's
	// processes at once, that margin was observed to run out. The budget is
	// derived from the test's own deadline instead of a bigger guessed
	// literal: a real bug here (the parent context DID kill the finisher)
	// still never creates donePath, so no budget lets that regression pass
	// by accident — it only decides how long the failure takes to name itself.
	budget := 30 * time.Second
	if testDeadline, ok := t.Deadline(); ok {
		if remaining := time.Until(testDeadline) - time.Second; remaining < budget {
			budget = remaining
		}
	}
	deadline := time.Now().Add(budget)
	for {
		if _, err := os.Stat(donePath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("nohup finisher never completed within %s — a cancelled parent context killed it", budget)
		}
		time.Sleep(20 * time.Millisecond)
	}
	content, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	for _, field := range argv {
		if field == "-f" {
			t.Fatalf("nohup argv = %q carries setsid's -f flag; nohup takes no such flag", argv)
		}
	}
	want := []string{
		"/jail/pfm",
		"--config",
		"/jail/config/pfm.json",
		"internal",
		"kill-exit",
		"--engine",
		string(args.Engine),
		"--id",
		args.ID,
		"--path",
		args.DataPath,
		"--socket",
		args.SocketPath,
		"--socket-name",
		args.SocketName,
		"--pane",
		args.PaneID,
	}
	if strings.Join(argv, "\n") != strings.Join(want, "\n") {
		t.Fatalf("nohup argv = %q, want %q", argv, want)
	}
}
