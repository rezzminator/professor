//go:build jail

package herdr

// The entry's signal law is the subject: a re-executed test binary must really be killed by its second
// SIGTERM while its painter is still unwinding.

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

const indicatorsSignalHelperEnv = "PFM_HERDR_INDICATORS_SIGNAL_HELPER"

// TestIndicatorsEntrySecondSignalKillsAPainterStillUnwinding pins the entry's
// signal law: the first SIGTERM cancels the painter, and a painter still
// unwinding after it is killed by the next one, never left running.
func TestIndicatorsEntrySecondSignalKillsAPainterStillUnwinding(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), os.Args[0],
		"-test.run=^TestIndicatorsEntrySignalHelper$")
	cmd.Env = append(os.Environ(), indicatorsSignalHelperEnv+"=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	lines := bufio.NewScanner(stdout)
	if !scanTo(lines, "painting") {
		_ = cmd.Process.Kill()
		t.Fatalf("helper did not start painting: %q %v", lines.Text(), lines.Err())
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if !scanTo(lines, "cancelled") {
		_ = cmd.Process.Kill()
		t.Fatalf("first SIGTERM did not cancel the painter: %q %v", lines.Text(), lines.Err())
	}
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
				t.Fatalf("helper exit = %v, want killed by the second SIGTERM", err)
			}
			return
		case <-tick.C:
			_ = cmd.Process.Signal(syscall.SIGTERM) // the next signal, until one lands after the reset
		case <-deadline:
			_ = cmd.Process.Kill()
			<-exited
			t.Fatal("a painter still unwinding survived repeated SIGTERMs: every later signal was swallowed")
		}
	}
}

// scanTo reads lines until one is want: the helper's test binary may print
// lines of its own first.
func scanTo(lines *bufio.Scanner, want string) bool {
	for lines.Scan() {
		if lines.Text() == want {
			return true
		}
	}
	return false
}

// TestIndicatorsEntrySignalHelper is the helper process of the signal test: a
// painter that, once cancelled, never returns.
func TestIndicatorsEntrySignalHelper(t *testing.T) {
	if os.Getenv(indicatorsSignalHelperEnv) != "1" {
		return // runs only as the signal test's helper process
	}
	values, err := paths.Resolve()
	if err != nil {
		t.Fatalf("resolve the helper's jail: %v", err)
	}
	env := &paths.MapEnv{HomeDir: values.Home, Values: map[string]string{EnvSocket: "/run/herdr.sock"}}
	IndicatorsEntry(io.Discard, env, func(ctx context.Context, _, _ string, _ io.Writer) error {
		_, _ = os.Stdout.WriteString("painting\n")
		<-ctx.Done()
		_, _ = os.Stdout.WriteString("cancelled\n")
		time.Sleep(time.Hour) // a restore that never ends
		return nil
	})
	os.Exit(3)
}
