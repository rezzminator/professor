package hookentry

import (
	"context"

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

// InstallPing sends the event=install ping when the ping is on; offline
// (pfm install --offline) skips this install's ping only, the daily ping
// staying on. It prints nothing. A failed ping is logged by installping and
// never fails the install.
func InstallPing(ctx context.Context, offline bool, runtime config.Runtime, env paths.Env) {
	options := installping.ForRuntime(runtime, env)
	if offline || installping.Reason(options) != "" {
		return
	}
	_ = installPingInstall(ctx, options) // installping logs its own failure WARN
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
