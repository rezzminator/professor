package herdr

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestIndicatorsEntryHandsThePainterTheHooksSocketAndTheHerdrStateDir pins the
// `pfm internal herdr-indicators` entry a Herdr plugin hook execs: the socket
// comes from the hook's HERDR_SOCKET_PATH, the lock directory is pfm's Herdr
// state directory, a painter error is exit 1, and a home that will not resolve
// never reaches the painter.
func TestIndicatorsEntryHandsThePainterTheHooksSocketAndTheHerdrStateDir(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	cases := []struct {
		name       string
		env        *paths.MapEnv
		paintErr   error
		wantCode   int
		wantCalled bool
		wantSocket string
		wantStderr string
	}{
		{
			name:       "painted",
			env:        &paths.MapEnv{HomeDir: home, Values: map[string]string{EnvSocket: "/run/herdr.sock"}},
			wantCode:   0,
			wantCalled: true,
			wantSocket: "/run/herdr.sock",
		},
		{
			name:       "painter error is exit 1",
			env:        &paths.MapEnv{HomeDir: home, Values: map[string]string{EnvSocket: "/run/herdr.sock"}},
			paintErr:   errors.New("herdr socket /run/herdr.sock: dial: refused"),
			wantCode:   1,
			wantCalled: true,
			wantSocket: "/run/herdr.sock",
		},
		{
			name:       "unset socket still reaches the painter, which names it",
			env:        &paths.MapEnv{HomeDir: home},
			paintErr:   errors.New("herdr indicators: HERDR_SOCKET_PATH is not set"),
			wantCode:   1,
			wantCalled: true,
			wantSocket: "",
		},
		{
			name:       "home error",
			env:        &paths.MapEnv{HomeErr: errors.New("no home")},
			wantCode:   1,
			wantStderr: "pfm internal herdr-indicators: resolve home directory: no home",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var called bool
			var gotSocket, gotLockDir string
			paint := func(_ context.Context, socket, lockDir string, _ io.Writer) error {
				called, gotSocket, gotLockDir = true, socket, lockDir
				return tc.paintErr
			}
			var stderr bytes.Buffer
			code := IndicatorsEntry(&stderr, tc.env, paint)
			if code != tc.wantCode || called != tc.wantCalled {
				t.Fatalf("exit %d, painter called %v; want exit %d, called %v (stderr %q)",
					code, called, tc.wantCode, tc.wantCalled, stderr.String())
			}
			if !tc.wantCalled {
				if !strings.Contains(stderr.String(), tc.wantStderr) {
					t.Fatalf("stderr %q lacks %q", stderr.String(), tc.wantStderr)
				}
				return
			}
			if gotSocket != tc.wantSocket || gotLockDir != paths.HerdrLockDir(home) {
				t.Fatalf("painter got socket %q lockDir %q; want %q %q",
					gotSocket, gotLockDir, tc.wantSocket, paths.HerdrLockDir(home))
			}
		})
	}
}

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
