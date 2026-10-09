package costsink

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// sinkContext ends on SIGTERM or SIGINT (a logout, a shutdown), so the sink still drains and
// writes every pending half instead of dying with them.
func sinkContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
}

// RunSink is `pfm internal cost-sink --pid N --start S`: the detached child Arm starts. It serves
// the listening socket it inherited on descriptor 3 until process N (identified by its start time S)
// is gone, then drains and exits. Its log is the sink log `pfm doctor` shows the tail of.
func RunSink(args []string, stderr io.Writer, env paths.Env) int {
	flags := cli.NewFlagSet(
		"internal cost-sink",
		"usage: pfm internal cost-sink --pid N --start S  (listening socket on fd 3)",
		stderr,
	)
	pid := flags.Int("pid", 0, "pid of the chat process to serve")
	start := flags.Uint64("start", 0, "start time of that process, as gather reads it")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if *pid <= 0 || *start == 0 || flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	home, err := paths.HomeFrom(env)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal cost-sink: %v\n", err)
		return 1
	}
	logFile, err := openSinkLog(home)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal cost-sink: %v\n", err)
		return 1
	}
	defer func() {
		if err := logFile.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm internal cost-sink: close %s: %v\n", logFile.Name(), err)
		}
	}()
	signal.Ignore(syscall.SIGHUP, syscall.SIGPIPE)
	file := os.NewFile(listenerFD, "cost-sink-listener")
	listener, err := net.FileListener(file)
	_ = file.Close()
	if err != nil {
		fmt.Fprintf(logFile, "cost-sink: fd %d is not a listening socket: %v\n", listenerFD, err)
		return 1
	}
	cfg := Config{
		Listener: listener, LedgerDir: paths.CostLedgerDir(env, home), Log: logFile,
		PID: *pid, Start: *start, Proc: gather.NewProcFS(""),
	}
	ctx, stop := sinkContext()
	defer stop()
	if err := Meter(ctx, cfg); err != nil {
		fmt.Fprintf(logFile, "cost-sink: stopped on error: %v\n", err)
		return 1
	}
	return 0
}
