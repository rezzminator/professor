package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"hostops/pfm/internal/deps"
)

// writeRepositorySnapshot's git calls: a start failure wins over a nonzero
// exit, exactly as the bare subprocess error it replaces reported only one
// cause per command.
func writeRepositorySnapshot(writer io.Writer, runner deps.Runner) {
	git := func(args ...string) (result deps.RunResult, err error) {
		result, err = runner.Run(
			context.Background(),
			append([]string{deps.Executable("git")}, args...),
			deps.RunOptions{},
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
