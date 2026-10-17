# 2026-10-17 — Root and default umask hide real-user permission failures

Observed: the plugin-log scratch self-test passed in the fence, which runs as root under the default umask, while a non-root user could not report or rotate logs through the project's own mode 775 scratch directory created under umask 002. The unreadable-directory fixture also used mode 500, which still lets its owner read and search it.

Amend: scripts/test-plugin-logs.sh — private-directory self-tests pin the umask and run the happy path on a user-owned mode 775 directory as a non-root user, not only as root in the fence. Keep hostile-owner and world-writable-without-sticky rejection checks; permission-denial fixtures must actually deny their runner access.

Resolved: 2026-10-17 — scripts/test-plugin-logs.sh pins umask 002, exercises report and rotate with mode 775 scratch ancestry, isolates its scratch root, and uses mode 000 for denied access. The suite was watched failing before the fix and passing afterwards both inside the fence and on the host as the non-root user.
