package installer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

// pluginCommandEnvironment is the inherited environment with CLAUDE_CONFIG_DIR
// set to dir and the login default's sentinel dropped: the child runs on the
// dir pfm chose, so nothing may read it as the inherited default.
func pluginCommandEnvironment(dir string) []string {
	inherited := deps.EnvironmentWith(claudeConfigDirEnv, dir)
	environment := make([]string, 0, len(inherited))
	for _, entry := range inherited {
		if !strings.HasPrefix(entry, claudelaunch.ConfigDirDefaultEnv+"=") {
			environment = append(environment, entry)
		}
	}
	return environment
}

// runClaudePluginCommand runs one `claude plugin` command under
// claudePluginCommandTimeout, timed on the installer's claudePluginTimer.
func (installer *engine) runClaudePluginCommand(ctx context.Context, argv []string, options deps.RunOptions) error {
	return runPluginCommandWithin(
		ctx, installer.claudePluginTimer, installer.processRunner(), argv, options, claudePluginCommandTimeout,
	)
}

// runPluginCommandWithin runs argv in its own process group under timeout,
// any failure an error naming the command, its exit code and its stderr. A nil
// timer is the real context timeout; a test hands its fake clock to fire it.
func runPluginCommandWithin(
	ctx context.Context,
	timer clock.Clock,
	runner deps.Runner,
	argv []string,
	options deps.RunOptions,
	timeout time.Duration,
) error {
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if timer != nil {
		cmdCtx, cancel = clock.WithTimeout(ctx, timer, timeout)
	} else {
		cmdCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	options.ProcessGroup = true
	options.WaitDelay = claudePluginWaitDelay
	result, err := runner.Run(cmdCtx, argv, options)
	if cmdCtx.Err() != nil && (err != nil || result.ExitCode != 0) {
		if ctx.Err() != nil {
			return fmt.Errorf("%s: cancelled: %w", strings.Join(argv[1:], " "), ctx.Err())
		}
		return fmt.Errorf(
			"%s: timed out after %s: %w", strings.Join(argv[1:], " "), timeout, cmdCtx.Err(),
		)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", strings.Join(argv[1:], " "), err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf(
			"%s: exit %d stderr=%q",
			strings.Join(argv[1:], " "),
			result.ExitCode,
			strings.TrimSpace(string(result.Stderr)),
		)
	}
	return nil
}
