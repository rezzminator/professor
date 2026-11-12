package coverflush

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

// The real counter write is proven by the journey suite, whose summary.md names the processes that
// wrote counters; a test binary holds no coverage meta-data to write.
func TestFlushWritesTheCountersThenRaisesTheSignal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		writeErr error
		want     string
	}{
		{"write succeeds", nil, "write /cover; raise terminated"},
		{"write fails, the signal still ends the process", errors.New("disk full"), "write /cover; raise terminated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			flush(func(dir string) error {
				calls = append(calls, "write "+dir)
				return tc.writeErr
			}, "/cover", syscall.SIGTERM, make(chan os.Signal, 1), func(sig syscall.Signal) error {
				calls = append(calls, "raise "+sig.String())
				return nil
			})
			if got := strings.Join(calls, "; "); got != tc.want {
				t.Fatalf("calls = %q, want %q", got, tc.want)
			}
		})
	}
}

// A terminate delivered to a process that installed the flush reaches it: the write, then the raise of
// the same signal. With GOCOVERDIR unset nothing is watched, and the terminate reaches no second flush.
func TestInstallFlushesOnTheFirstTerminatingSignal(t *testing.T) {
	calls := make(chan string, 4)
	write := func(dir string) error { calls <- "write " + dir; return nil }
	raise := func(sig syscall.Signal) error { calls <- "raise " + sig.String(); return nil }
	install("", write, raise)
	install("/cover", write, raise)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM to the test process: %v", err)
	}
	var got []string
	for len(got) < 2 {
		select {
		case call := <-calls:
			got = append(got, call)
		case <-time.After(5 * time.Second):
			t.Fatalf("calls after SIGTERM = %q within 5s, want [write /cover raise terminated]", got)
		}
	}
	if strings.Join(got, "; ") != "write /cover; raise terminated" {
		t.Fatalf("calls after SIGTERM = %q, want [write /cover raise terminated]", got)
	}
	select {
	case extra := <-calls:
		t.Fatalf("a second flush ran: %q (the empty-dir install watched a signal)", extra)
	case <-time.After(100 * time.Millisecond):
	}
}
