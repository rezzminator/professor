// Package coverflush makes an instrumented pfm (`-cover -covermode=atomic -tags=pfmcover`, the
// journey machine's) write its coverage counters when a hangup, an interrupt or a terminate ends it:
// Go writes them only on a normal exit, so a process ended by one of those signals would write none.
// A normal build links this package with nothing installed and no signal watched.
//
// init_pfmcover.go: the instrumented build's init, which installs the flush under GOCOVERDIR.
package coverflush

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// install watches the terminating signals the process did not start with ignored and, on the first
// one, flushes into dir. Nothing is watched when dir is empty (GOCOVERDIR unset).
func install(dir string, write func(dir string) error, raise func(syscall.Signal) error) {
	if dir == "" {
		return
	}
	var watched []os.Signal
	for _, sig := range []os.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM} {
		if !signal.Ignored(sig) {
			watched = append(watched, sig)
		}
	}
	if len(watched) == 0 {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, watched...)
	go func() {
		sig := (<-signals).(syscall.Signal)
		flush(write, dir, sig, signals, raise)
	}()
}

// flush writes the counters into dir (write is runtime/coverage.WriteCountersDir, which a test binary
// cannot run: it holds no meta-data), stops watching, and raises sig again: with no handler of the
// process's own left, the signal ends it as it would have without the flush. A raise that fails exits
// with the shell's code for that signal.
func flush(
	write func(dir string) error, dir string, sig syscall.Signal, signals chan os.Signal,
	raise func(syscall.Signal) error,
) {
	if err := write(dir); err != nil {
		obs.Logger(context.Background()).Error("coverage flush", "signal", sig.String(), "dir", dir, obs.FieldErr, err)
	}
	signal.Stop(signals)
	if err := raise(sig); err != nil {
		obs.Logger(context.Background()).Error("coverage flush: raise the signal again", "signal", sig.String(),
			obs.FieldErr, err)
		os.Exit(128 + int(sig))
	}
}
