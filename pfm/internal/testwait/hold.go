package testwait

// holdCommand blocks until a signal ends it and never writes. tail follows a
// file that never grows, so no clock paces it; its stderr goes to /dev/null
// because GNU and uutils tail print `tail: inotify cannot be used, reverting to
// polling: Too many open files` there when the host has no inotify instance to
// spare (a CI runner), a foreign line in the pane or captured stderr a test
// reads. `9>&2` first keeps the caller's stderr open in the holder, unwritten,
// so a hold standing in for a grandchild that pins a pipe still pins it.
const holdCommand = "tail -f /dev/null 9>&2 2>/dev/null"

// Hold is the shell command of a child a test keeps alive until it kills it: a
// pane's program, a fake's grandchild, a process to signal. It execs, so the
// pid a shell reports ($!, a pane's pid) is tail's own: sh forks a last
// command that carries a redirect instead of exec'ing it. Its shell twin is
// shtest_hold in scripts/shtest-wait.sh.
const Hold = "exec " + holdCommand

// HoldAs is Hold under argv[0] name, for a fixture a process lookup finds by
// that name; bash only (exec -a).
func HoldAs(name string) string {
	return "exec -a " + name + " " + holdCommand
}

// HoldArgv is the hold's argv for a direct child, no shell between, so a
// process lookup reads tail's own argv from the first instant; start it with
// Stderr nil (os/exec's /dev/null).
func HoldArgv() []string {
	return []string{"tail", "-f", "/dev/null"}
}
