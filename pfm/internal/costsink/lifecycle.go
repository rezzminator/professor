package costsink

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/costledger"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

// Defaults of Config's zero fields.
const (
	DefaultPollEvery       = 2 * time.Second
	DefaultFlushEvery      = time.Second
	DefaultDrainIdle       = 3 * time.Second
	DefaultDrainMax        = 15 * time.Second
	DefaultPruneAfter      = 90 * 24 * time.Hour
	DefaultMaxStatFailures = 30
)

// Config is one sink run: the listening socket it was handed, where the ledger lives, and the
// identity of the chat process it serves (pid plus start time, so a reused pid is another process).
type Config struct {
	Listener  net.Listener
	LedgerDir string
	Log       io.Writer
	PID       int
	Start     uint64
	Proc      gather.ProcFS

	PollEvery, FlushEvery time.Duration
	// DrainIdle is how long after the last POST a sink whose chat is gone keeps accepting; DrainMax
	// caps the whole drain.
	DrainIdle, DrainMax time.Duration
	Wait, PruneAfter    time.Duration
	MaxBody             int64
	// MaxStatFailures is how many consecutive unreadable process-table reads end the run.
	MaxStatFailures int
}

func (c *Config) defaults() {
	set := func(v *time.Duration, d time.Duration) {
		if *v <= 0 {
			*v = d
		}
	}
	set(&c.PollEvery, DefaultPollEvery)
	set(&c.FlushEvery, DefaultFlushEvery)
	set(&c.DrainIdle, DefaultDrainIdle)
	set(&c.DrainMax, DefaultDrainMax)
	set(&c.Wait, DefaultWait)
	set(&c.PruneAfter, DefaultPruneAfter)
	if c.MaxStatFailures <= 0 {
		c.MaxStatFailures = DefaultMaxStatFailures
	}
}

// Meter serves cfg.Listener until the watched process is gone, then drains: it keeps accepting until
// DrainIdle passes without a POST (at most DrainMax), writes every pending half and returns. The
// ledger directory is pruned of files older than PruneAfter first. It returns an error only when the
// process table stayed unreadable or the server failed; the cause is also in the sink log.
func Meter(ctx context.Context, cfg Config) error {
	cfg.defaults()
	sink := NewSink(SinkOptions{Dir: cfg.LedgerDir, Log: cfg.Log, Wait: cfg.Wait, MaxBody: cfg.MaxBody})
	if removed, err := costledger.Prune(cfg.LedgerDir, clock.Real.Now(), cfg.PruneAfter); err != nil {
		sink.Logf("prune of %s failed after %d removals: %v", cfg.LedgerDir, removed, err)
	}
	server := &http.Server{Handler: sink, ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- server.Serve(cfg.Listener) }()
	sink.Logf("serving %s for pid %d (start time %d)", cfg.Listener.Addr(), cfg.PID, cfg.Start)

	runErr := waitForChatExit(ctx, cfg, sink, served)
	drain(cfg, sink)
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		sink.Logf("shutdown: %v", err)
	}
	sink.Drain()
	sink.Logf("stopped (%d faults)", sink.Faults())
	return runErr
}

// waitForChatExit blocks until the chat process is gone, the context ends, the server fails or the process
// table stays unreadable; it flushes the join on every flush tick.
func waitForChatExit(ctx context.Context, cfg Config, sink *Sink, served <-chan error) error {
	poll, flush := clock.Real.NewTicker(cfg.PollEvery), clock.Real.NewTicker(cfg.FlushEvery)
	defer poll.Stop()
	defer flush.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-served:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				sink.Logf("server stopped: %v", err)
				return fmt.Errorf("cost sink server: %w", err)
			}
			return nil
		case <-flush.C():
			sink.Flush()
		case <-poll.C():
			stat, err := cfg.Proc.Stat(cfg.PID)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				sink.Logf("pid %d is gone", cfg.PID)
				return nil
			case err != nil:
				failures++
				sink.Logf("read pid %d (%d of %d tolerated): %v", cfg.PID, failures, cfg.MaxStatFailures, err)
				if failures >= cfg.MaxStatFailures {
					return fmt.Errorf("cost sink: read process %d %d times in a row: %w", cfg.PID, failures, err)
				}
			case stat.StartTime != cfg.Start:
				sink.Logf("pid %d now has start time %d, not %d: the chat is gone", cfg.PID, stat.StartTime, cfg.Start)
				return nil
			default:
				failures = 0
			}
		}
	}
}

// drain keeps the server accepting until no POST has arrived for DrainIdle, at most DrainMax: the
// chat's exporters flush on exit and a late batch must still land.
func drain(cfg Config, sink *Sink) {
	started := clock.Real.Now()
	for clock.Real.Now().Sub(started) < cfg.DrainMax {
		// The window opens when the chat was found gone, whatever arrived before it.
		last := sink.LastPost()
		if last.Before(started) {
			last = started
		}
		if clock.Real.Now().Sub(last) >= cfg.DrainIdle {
			return
		}
		_ = clock.Real.Sleep(context.Background(), cfg.FlushEvery/4)
	}
}
