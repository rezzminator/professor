package hookentry

import (
	"context"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installping"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// installPingInstall and installPingDaily send the two install pings; tests
// replace them.
var (
	installPingInstall = installping.SendInstall
	installPingDaily   = installping.SendDaily
)

// InstallPing prints pfm install's one install-ping line and, when the ping
// is on, sends the event=install ping; offline (pfm install --offline) skips
// this install's ping only, the daily ping staying on. A failed ping is logged
// by installping and never fails the install.
func InstallPing(ctx context.Context, stdout io.Writer, offline bool, runtime config.Runtime, env paths.Env) {
	options := installping.ForRuntime(runtime, env)
	reason := installping.Reason(options.Env, options.Version)
	switch {
	case reason != "":
		fmt.Fprintln(stdout, installping.Notice(reason))
	case offline:
		fmt.Fprintln(stdout, installping.NoticeSkipped("--offline"))
	default:
		fmt.Fprintln(stdout, installping.Notice(""))
		_ = installPingInstall(ctx, options) // installping logs its own failure WARN
	}
}

// DailyInstallPingDuring sends the event=daily install ping while work runs
// and returns work's code once both have finished. Its caller is each chat's
// long-lived paced statusline worker, the one detached run every chat launch
// starts: a worker that exits at once (another holds the lock) waits out the
// ping, bounded by installping.Timeout, so a claimed day is never lost.
func DailyInstallPingDuring(ctx context.Context, runtime config.Runtime, env paths.Env, work func() int) int {
	options := installping.ForRuntime(runtime, env)
	pinged := make(chan struct{})
	go func() {
		defer close(pinged)
		_ = installPingDaily(ctx, options) // installping logs its own failure WARN
	}()
	code := work()
	<-pinged
	return code
}
