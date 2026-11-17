package main

import (
	"context"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/installping"
)

// installPingInstall sends pfm install's event=install ping; tests replace it.
var installPingInstall = installping.SendInstall

// announceInstallPing prints pfm install's one install-ping line and, when the
// ping is on, sends the event=install ping; --offline skips this install's ping
// only, the daily ping staying on. A failed ping is logged by installping and
// never fails the install.
func announceInstallPing(ctx context.Context, stdout io.Writer, offline bool, options installping.Options) {
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
