//go:build jail

package installer

// A plugin command that hangs is stopped with its whole process group: the real children and their pids are the subject.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// recordedPIDs reads every pid a fixture script appended to path.
func recordedPIDs(t *testing.T, path string) []int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pids []int
	for _, field := range strings.Fields(string(raw)) {
		pid, convErr := strconv.Atoi(field)
		if convErr != nil {
			t.Fatalf("parse recorded pid %q: %v", field, convErr)
		}
		pids = append(pids, pid)
	}
	return pids
}

func TestClaudePluginCommandTimeoutKillsTheWholeProcessGroup(t *testing.T) {
	home := t.TempDir()
	account := filepath.Join(home, ".cc", "1") // not the store: the command then carries CLAUDE_CONFIG_DIR
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	// The fake claude records its own pid and a backgrounded sleep's pid under
	// the account dir (the one path the command's environment carries), then
	// hangs, the way a stuck plugin command does.
	binary := writeScript(t, t.TempDir(), "claude-hang", "#!/bin/sh\n"+
		"sleep 300 &\n"+
		"echo $! >> \"$CLAUDE_CONFIG_DIR/child.pids\"\n"+
		"echo $$ >> \"$CLAUDE_CONFIG_DIR/main.pids\"\n"+
		"exec sleep 300\n")
	t.Cleanup(func() {
		for _, name := range []string{"child.pids", "main.pids"} {
			for _, pid := range recordedPIDs(t, filepath.Join(account, name)) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	previous := claudePluginTimeout
	claudePluginTimeout = 300 * time.Millisecond
	t.Cleanup(func() { claudePluginTimeout = previous })

	var out bytes.Buffer
	installer := &engine{options: Options{
		Home: home, ConfigDir: account, ClaudeBinary: binary, Stdout: &out,
		ClaudePlugins: pfmconfig.DefaultClaudePlugins(),
	}, apply: true}
	finished := make(chan error, 1)
	go func() { finished <- installer.ensureClaudePlugins(context.Background()) }()
	var err error
	select {
	case err = <-finished:
	case <-time.After(15 * time.Second):
		t.Fatal("ensureClaudePlugins did not return within 15s: the hung command was not stopped")
	}
	want := "claude plugin cache-live-control@professor in " + account +
		": plugin marketplace add rezzminator/professor: timed out after 300ms: context deadline exceeded"
	if err == nil || strings.Split(err.Error(), "\n")[0] != want {
		t.Fatalf("error=%v, want its first line %q", err, want)
	}
	pids := append(recordedPIDs(t, filepath.Join(account, "child.pids")),
		recordedPIDs(t, filepath.Join(account, "main.pids"))...)
	if len(pids) < 2 {
		t.Fatalf("fixture recorded pids %v, want a shell and a backgrounded child", pids)
	}
	for _, pid := range pids {
		deadline := time.Now().Add(5 * time.Second)
		for syscall.Kill(pid, 0) == nil {
			if time.Now().After(deadline) {
				t.Fatalf("process %d survived the plugin command timeout", pid)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}
