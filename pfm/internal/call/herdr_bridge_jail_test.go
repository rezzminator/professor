package call

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestHerdrBridgeRecordReadOnARealServer reads the session record through a
// real tmux server, the producer a fixture cannot stand in for: tmux 3.x reads
// a bare "=name" target as no session, and -q turned that into an empty
// record. The exact-session target reads the attached session's own record,
// never a session its name prefixes, and a failed read is an error naming
// tmux's reason, never "no record".
func TestHerdrBridgeRecordReadOnARealServer(t *testing.T) {
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
	tmux("new-session", "-d", "-s", "cc-1", "sleep", "120")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socketPath, "kill-server").Run() })
	tmux("new-session", "-d", "-s", "cc-10", "sleep", "120")
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
