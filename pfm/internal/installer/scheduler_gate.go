package installer

import (
	"context"
	"strings"
)

// nameSyncServiceRunning reports whether the Linux name-sync service is
// executing right now, and whether the question could be asked at all.
//
// pfm-name-sync.service is Type=oneshot: while it runs its state is
// "activating", and `systemctl is-active` exits 3 for that exactly as it does
// for "inactive" — an exit-code gate reads a running job as idle and can never
// refuse. So the state is read by name from `systemctl show`: a unit doing work
// (active, activating, deactivating, reloading, refreshing) is running;
// inactive or failed is idle (systemd reports an unknown unit as inactive).
// Anything else — a non-zero exit such as "Failed to connect to bus", systemctl
// missing from PATH, a runner that cannot read output, a state this gate does
// not know — means the probe never got an answer, and the caller must not read
// that silence as safety.
func nameSyncServiceRunning(ctx context.Context, runner CommandRunner) (running, probed bool) {
	reader, ok := runner.(OutputRunner)
	if !ok {
		return false, false
	}
	output, err := reader.Output(
		ctx, "systemctl", "--user", "show", "--property=ActiveState", "--value", "pfm-name-sync.service",
	)
	if err != nil {
		return false, false
	}
	switch strings.TrimSpace(string(output)) {
	case "active", "activating", "deactivating", "reloading", "refreshing":
		return true, true
	case "inactive", "failed":
		return false, true
	default:
		return false, false
	}
}
