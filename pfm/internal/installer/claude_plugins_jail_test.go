//go:build jail

package installer

// A plugin command that hangs is stopped with its whole process group: the real children and their pids are the subject.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/testwait"
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
	// The fake claude records its own pid and a backgrounded tail's pid under
	// the account dir (the one path the command's environment carries), then
	// hangs, the way a stuck plugin command does.
	binary := writeScript(t, t.TempDir(), "claude-hang", "#!/bin/sh\n"+
		testwait.Hold+" &\n"+
		"echo $! >> \"$CLAUDE_CONFIG_DIR/child.pids\"\n"+
		"echo $$ >> \"$CLAUDE_CONFIG_DIR/main.pids\"\n"+
		testwait.Hold+"\n")
	t.Cleanup(func() {
		for _, name := range []string{"child.pids", "main.pids"} {
			for _, pid := range recordedPIDs(t, filepath.Join(account, name)) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	// The command's deadline is timed on a fake, moved to it once the hung
	// command and its backgrounded child are both up.
	fake := clock.NewFake(time.Unix(0, 0))
	var out bytes.Buffer
	installer := &engine{options: Options{
		Home: home, ConfigDir: account, ClaudeBinary: binary, Stdout: &out,
		ClaudePlugins: pfmconfig.DefaultClaudePlugins(),
	}, apply: true, claudePluginTimer: fake}
	finished := make(chan error, 1)
	go func() { finished <- installer.ensureClaudePlugins(context.Background()) }()
	testwait.Until(t, "the hung plugin command and its child to record their pids", func() (bool, string) {
		child := recordedPIDs(t, filepath.Join(account, "child.pids"))
		leader := recordedPIDs(t, filepath.Join(account, "main.pids"))
		return len(child) == 1 && len(leader) == 1, fmt.Sprintf("child %v leader %v", child, leader)
	})
	if pending := fake.Pending(); pending != 1 {
		t.Fatalf("armed deadlines = %d (%s), want the hung command's one", pending, fake)
	}
	fake.Advance(claudePluginCommandTimeout - time.Nanosecond)
	if pending := fake.Pending(); pending != 1 {
		t.Fatalf("the deadline fired %s before %s: %s", time.Nanosecond, claudePluginCommandTimeout, fake)
	}
	fake.Advance(time.Nanosecond)
	// Every later plugin command runs the same hung fake: each is ended at its
	// own deadline as it arms it, until the install returns.
	testwait.Until(t, "ensureClaudePlugins returned: every hung command was stopped", func() (bool, string) {
		if len(finished) == 1 {
			return true, ""
		}
		if fake.Pending() == 1 {
			fake.Advance(claudePluginCommandTimeout)
		}
		return false, fake.String()
	})
	err := <-finished
	want := "claude plugin cache-live-control@professor in " + account +
		": plugin marketplace add rezzminator/professor: timed out after 2m0s: context deadline exceeded"
	if err == nil || strings.Split(err.Error(), "\n")[0] != want {
		t.Fatalf("error=%v, want its first line %q", err, want)
	}
	pids := append(recordedPIDs(t, filepath.Join(account, "child.pids")),
		recordedPIDs(t, filepath.Join(account, "main.pids"))...)
	if len(pids) < 2 {
		t.Fatalf("fixture recorded pids %v, want a shell and a backgrounded child", pids)
	}
	for _, pid := range pids {
		testwait.Until(t, fmt.Sprintf("process %d died with the plugin command timeout", pid), func() (bool, string) {
			return syscall.Kill(pid, 0) != nil, "process survived the plugin command timeout"
		})
	}
}
