package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func runChatSave(args []string, stdout, stderr io.Writer, env paths.Env, runtimes ...commandRuntime) (exitCode int) {
	return runChatSaveContext(context.Background(), args, stdout, stderr, env, runtimes...)
}

// writeRepositorySnapshot's git calls: a start failure wins over a nonzero
// exit, exactly as the bare subprocess error it replaces reported only one
// cause per command.
func writeRepositorySnapshot(ctx context.Context, writer io.Writer, runner deps.Runner) {
	self, _ := pfmchat.ScopedSelf(ctx)
	git := func(args ...string) (result deps.RunResult, err error) {
		result, err = runner.Run(
			ctx,
			append([]string{deps.Executable("git")}, args...),
			deps.RunOptions{Dir: self.CWD},
		)
		if err == nil && result.ExitCode != 0 {
			err = fmt.Errorf("exit status %d", result.ExitCode)
		}
		return result, err
	}
	if _, err := git("rev-parse", "--is-inside-work-tree"); err != nil {
		fmt.Fprintln(writer, "(not a git repository)")
		return
	}
	branch, branchErr := git(branchAction, "--show-current")
	status, statusErr := git("status", "--short")
	worktrees, worktreeErr := git("worktree", "list")
	if branchErr != nil || statusErr != nil || worktreeErr != nil {
		fmt.Fprintf(
			writer,
			"(repository snapshot failed: branch=%v status=%v worktrees=%v)\n",
			branchErr, statusErr, worktreeErr,
		)
		return
	}
	fmt.Fprintf(
		writer,
		"Branch: %s\n\n```\n%s```\n\nWorktrees:\n```\n%s```\n",
		strings.TrimSpace(string(branch.Stdout)),
		status.Stdout,
		worktrees.Stdout,
	)
}
