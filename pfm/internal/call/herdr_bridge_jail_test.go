//go:build jail

package call

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestHerdrBridgeRecordReadOnARealServer reads the session record through a
// real tmux server, the producer a fixture cannot stand in for: tmux 3.x reads
// a bare "=name" target as no session, and -q turned that into an empty
// record. The exact-session target reads the attached session's own record,
// never a session its name prefixes, and a failed read is an error naming
// tmux's reason, never "no record".
func TestHerdrBridgeRecordReadOnARealServer(t *testing.T) {
	testjail.TmuxServer(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := testjail.ShortRoot(t)
	socketPath := filepath.Join(root, "cc-1")
	tmux := func(args ...string) {
		t.Helper()
		full := append([]string{"-S", socketPath, "-f", "/dev/null"}, args...)
		if output, err := exec.Command("tmux", full...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %q: %v: %s", args, err, output)
		}
	}
	tmux("new-session", "-d", "-s", "cc-1", testwait.Hold)
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socketPath, "kill-server").Run() })
	tmux("new-session", "-d", "-s", "cc-10", testwait.Hold)
	tmux("set-option", "-t", "cc-10:", "@pfm_herdr", `{"session_id":"other"}`)
	for _, tc := range []struct {
		name, session, record, wantErr, refuse string
	}{
		{name: "proved", session: "cc-1", record: `{"session_id":"s1"}`},
		{
			name: "a prefix session's record is not this one's, and re-attach is the fix", session: "cc-1",
			wantErr: "holds no pfm Herdr record: re-attach",
		},
		{
			name: "a failed read names tmux's reason", session: "gone", wantErr: "read @pfm_herdr of session gone",
			refuse: "holds no pfm Herdr record",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.record != "" {
				tmux("set-option", "-t", "cc-1:", "@pfm_herdr", tc.record)
			} else {
				tmux("set-option", "-u", "-t", "cc-1:", "@pfm_herdr")
			}
			prover := bridgeProver{
				values: paths.Values{TmuxDir: root},
				proc: &bridgeProc{
					argv:   map[int][]string{10730: {"tmux", "-S", socketPath, "attach"}},
					starts: map[int][]uint64{10730: {9}},
					reads:  map[int]int{},
				},
				foreground: func(context.Context, string, string) ([]int, error) { return []int{10730}, nil },
				tmux: func(ctx context.Context, server string, args ...string) (string, error) {
					if args[0] == "list-clients" {
						return "10730\x1f" + tc.session + "\n", nil
					}
					return bridgeTmux(ctx, server, args...)
				},
			}
			got, err := prover.prove(context.Background(), "/fixture/herdr.sock", "w6:p3")
			if tc.wantErr == "" {
				want := HerdrBridge{TmuxSocket: socketPath, Session: "cc-1", ClientPID: 10730}
				if err != nil || got != want {
					t.Fatalf("bridge = %+v, %v; want %+v", got, err, want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) ||
				tc.refuse != "" && strings.Contains(err.Error(), tc.refuse) {
				t.Fatalf("bridge = %+v, %v; want an error naming %q", got, err, tc.wantErr)
			}
		})
	}
}

// TestHerdrBridgeTmuxGivesUpOnAFrozenServer pins the bridge read's bound: a
// pfm tmux server that accepts and never answers fails the proof within
// gather.ProbeTimeout, so it never stalls the painter that asked.
func TestHerdrBridgeTmuxGivesUpOnAFrozenServer(t *testing.T) {
	testjail.TmuxServer(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := testjail.ShortRoot(t)
	// tmux's own refusal reason reaches the proof's error, not only its exit status.
	if _, err := bridgeTmux(context.Background(), filepath.Join(root, "gone"), "list-clients"); err == nil ||
		(!strings.Contains(err.Error(), "no server running") && !strings.Contains(err.Error(), "error connecting")) {
		t.Fatalf("read of a missing server = %v, want tmux's refusal reason", err)
	}
	socketPath := filepath.Join(root, "cc-1")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	held := make(chan net.Conn, 16)
	t.Cleanup(func() {
		_ = listener.Close()
		for len(held) > 0 {
			_ = (<-held).Close()
		}
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			held <- conn // held open, never answered
		}
	}()
	// The probe bound fires on a fake clock the test moves, and the killed
	// client's pipes, held by the frozen server, close at once: the test waits
	// out neither in real time.
	fake := clock.NewFake(time.Unix(0, 0))
	keptDelay := bridgeTmuxWaitDelay
	bridgeTmuxWaitDelay = time.Nanosecond
	t.Cleanup(func() { bridgeTmuxWaitDelay = keptDelay })
	done := make(chan error, 1)
	go func() {
		_, err := bridgeTmux(gather.WithProbeClock(context.Background(), fake), socketPath,
			"list-clients", "-F", "#{client_pid}")
		done <- err
	}()
	testwait.Until(t, "the read to arm its probe bound and reach the frozen server", func() (bool, string) {
		return fake.Pending() == 1 && len(held) > 0,
			fmt.Sprintf("pending=%d accepted=%d clock=%s", fake.Pending(), len(held), fake)
	})
	fake.Advance(gather.ProbeTimeout - time.Nanosecond)
	if fake.Pending() != 1 {
		t.Fatalf("the probe bound fired %s before %s: %s", time.Nanosecond, gather.ProbeTimeout, fake)
	}
	fake.Advance(time.Nanosecond)
	err, _ = testwait.Receive(t, "the read of a frozen server to end", done)
	if !errors.Is(err, gather.ErrServerUnresponsive) {
		t.Fatalf(
			"read of a frozen server = %v, want %v within %s",
			err,
			gather.ErrServerUnresponsive,
			gather.ProbeTimeout,
		)
	}
}
