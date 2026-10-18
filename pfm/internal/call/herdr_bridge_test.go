package call

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// bridgeProc is a ProcFS whose Stat answers walk starts[pid] one read at a time.
type bridgeProc struct {
	argv   map[int][]string
	starts map[int][]uint64
	reads  map[int]int
}

func (proc *bridgeProc) PIDs() ([]int, error) { return nil, errors.New("PIDs unused") }

func (proc *bridgeProc) Cmdline(pid int) ([]string, error) {
	argv, ok := proc.argv[pid]
	if !ok {
		return nil, fmt.Errorf("pid %d: %w", pid, os.ErrNotExist)
	}
	return argv, nil
}

func (proc *bridgeProc) Environ(int) (map[string]string, error) {
	return nil, errors.New("Environ unused")
}

func (proc *bridgeProc) FDLinks(int) ([]gather.FDLink, error) {
	return nil, errors.New("FDLinks unused")
}

func (proc *bridgeProc) Stat(pid int) (gather.ProcStat, error) {
	starts := proc.starts[pid]
	if len(starts) == 0 {
		return gather.ProcStat{}, fmt.Errorf("pid %d: %w", pid, os.ErrNotExist)
	}
	read := min(proc.reads[pid], len(starts)-1)
	proc.reads[pid]++
	return gather.ProcStat{ParentPID: 1, StartTime: starts[read]}, nil
}

func TestHerdrBridgeProof(t *testing.T) {
	const tmuxDir = "/fixture/tmux-501"
	clientArgv := []string{"tmux", "-L", "cc-1", "attach", "-t", "cc-1"}
	for _, tc := range []struct {
		name          string
		foreground    []int
		foregroundErr error
		argv          []string
		starts        []uint64
		clients       string
		clientsErr    error
		stored        string
		want          HerdrBridge
		wantErr       string
		wantTmux      bool
	}{
		{
			name: "proved", argv: clientArgv, starts: []uint64{9}, clients: "10730\x1fcc-1\n",
			stored: `{"session_id":"s1"}`, wantTmux: true,
			want: HerdrBridge{TmuxSocket: tmuxDir + "/cc-1", Session: "cc-1", ClientPID: 10730},
		},
		{
			name: "socket-path-inside", argv: []string{"tmux", "-S", tmuxDir + "/cc-1", "attach"}, starts: []uint64{9},
			clients: "10730\x1fcc-1\n", stored: `{"session_id":"s1"}`, wantTmux: true,
			want: HerdrBridge{TmuxSocket: tmuxDir + "/cc-1", Session: "cc-1", ClientPID: 10730},
		},
		{name: "not-tmux", argv: []string{"vim", "-L", "cc-1"}, starts: []uint64{9}, wantErr: "not a tmux client"},
		{
			name: "socket-path-outside", argv: []string{"tmux", "-S", "/elsewhere/cc-1", "attach"},
			starts: []uint64{9}, wantErr: "outside pfm's tmux directory",
		},
		{
			name: "socket-path-escapes", argv: []string{"tmux", "-S", tmuxDir + "/../elsewhere/cc-1", "attach"},
			starts: []uint64{9}, wantErr: "outside pfm's tmux directory",
		},
		{name: "default-server", argv: []string{"tmux", "attach"}, starts: []uint64{9}, wantErr: "names no pfm tmux server"},
		{
			name: "not-attached", argv: clientArgv, starts: []uint64{9}, clients: "999\x1fcc-1\n", wantTmux: true,
			wantErr: "not attached",
		},
		{
			name: "server-refuses", argv: clientArgv, starts: []uint64{9}, clientsErr: errors.New("no server"),
			wantTmux: true, wantErr: "no server",
		},
		{
			name: "no-record", argv: clientArgv, starts: []uint64{9}, clients: "10730\x1fcc-1\n", stored: "\n",
			wantTmux: true, wantErr: "no pfm Herdr record",
		},
		{
			name: "corrupt-record", argv: clientArgv, starts: []uint64{9}, clients: "10730\x1fcc-1\n", stored: "{",
			wantTmux: true, wantErr: "decode",
		},
		{
			name: "empty-record", argv: clientArgv, starts: []uint64{9}, clients: "10730\x1fcc-1\n", stored: "{}",
			wantTmux: true, wantErr: "empty pfm Herdr record",
		},
		{
			name: "empty-clients-record", argv: clientArgv, starts: []uint64{9}, clients: "10730\x1fcc-1\n",
			stored: `{"clients":{}}`, wantTmux: true, wantErr: "empty pfm Herdr record",
		},
		{
			name: "null-record", argv: clientArgv, starts: []uint64{9}, clients: "10730\x1fcc-1\n", stored: "null",
			wantTmux: true, wantErr: "empty pfm Herdr record",
		},
		{
			name: "restarted", argv: clientArgv, starts: []uint64{9, 10}, clients: "10730\x1fcc-1\n",
			stored: `{"session_id":"s1"}`, wantTmux: true, wantErr: "start identity changed",
		},
		{name: "gone", argv: clientArgv, wantErr: "file does not exist"},
		{name: "empty-foreground", foreground: []int{}, wantErr: "no pfm tmux client"},
		{name: "foreground-error", foregroundErr: errors.New("socket refused"), wantErr: "socket refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			foreground := tc.foreground
			if foreground == nil {
				foreground = []int{10284, 10730}
			}
			proc := &bridgeProc{
				argv:   map[int][]string{10284: {"-zsh"}, 10730: tc.argv},
				starts: map[int][]uint64{10284: {1}, 10730: tc.starts},
				reads:  map[int]int{},
			}
			var tmuxCalls []string
			prover := bridgeProver{
				values: paths.Values{TmuxDir: tmuxDir},
				proc:   proc,
				foreground: func(_ context.Context, socket, pane string) ([]int, error) {
					if socket != "/fixture/herdr.sock" || pane != "w6:p3" {
						t.Errorf("foreground of %s %s", socket, pane)
					}
					return foreground, tc.foregroundErr
				},
				tmux: func(_ context.Context, socketPath string, args ...string) (string, error) {
					tmuxCalls = append(tmuxCalls, socketPath+" "+strings.Join(args, " "))
					if socketPath != filepath.Join(tmuxDir, "cc-1") {
						return "", fmt.Errorf("unexpected server %s", socketPath)
					}
					switch args[0] {
					case "list-clients":
						return tc.clients, tc.clientsErr
					case "show-options":
						if strings.Join(args, " ") != "show-options -v -t =cc-1: @pfm_herdr" {
							t.Errorf("record read %q", args)
						}
						return tc.stored, nil
					}
					return "", fmt.Errorf("unexpected tmux %q", args)
				},
			}
			got, err := prover.prove(context.Background(), "/fixture/herdr.sock", "w6:p3")
			if tc.wantErr == "" {
				if err != nil || got != tc.want {
					t.Fatalf("bridge = %+v, %v; want %+v", got, err, tc.want)
				}
			} else if err == nil || !strings.Contains(err.Error(), "ownership unproved") ||
				!strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("bridge = %+v, %v; want ownership unproved naming %q", got, err, tc.wantErr)
			}
			if (len(tmuxCalls) > 0) != tc.wantTmux {
				t.Fatalf("tmux calls %q; want any=%t", tmuxCalls, tc.wantTmux)
			}
		})
	}
}

func TestHerdrPaneProofAcceptsBridge(t *testing.T) {
	seedNativeOwner(t, Target{SocketPath: "/fixture/other.sock", Engine: "cc"})
	for _, tc := range []struct {
		name      string
		bridgeErr error
		wantErr   []string
	}{
		{name: "bridge-proved"},
		{
			name: "neither", bridgeErr: errors.New("bridge refused"),
			wantErr: []string{"no durable owner", "bridge refused"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked string
			err := proveHerdrPaneOrBridge(
				context.Background(),
				"/fixture/herdr.sock",
				"w6:p3",
				func(_ context.Context, socket, pane string) (HerdrBridge, error) {
					asked = socket + " " + pane
					return HerdrBridge{TmuxSocket: "/fixture/tmux/cc-1", Session: "cc-1", ClientPID: 7}, tc.bridgeErr
				},
			)
			if asked != "/fixture/herdr.sock w6:p3" {
				t.Fatalf("bridge asked %q", asked)
			}
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("bridge pane proof = %v; want proved", err)
				}
				return
			}
			for _, want := range append(tc.wantErr, "ownership unproved") {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("unproved pane = %v; want %q", err, want)
				}
			}
		})
	}
}

// TestHerdrBridgeForegroundIsThatPanes pins the proof's seed: the foreground
// pids come only from a pane.process_info reply naming the asked pane.
func TestHerdrBridgeForegroundIsThatPanes(t *testing.T) {
	for _, tc := range []struct {
		name, reply, wantErr string
		want                 []int
	}{
		{
			name:  "same-pane",
			reply: `{"type":"pane_process_info","process_info":{"pane_id":"w6:p3","foreground_processes":[{"pid":10730}]}}`,
			want:  []int{10730},
		},
		{
			name:    "other-pane",
			reply:   `{"type":"pane_process_info","process_info":{"pane_id":"w6:p4","foreground_processes":[{"pid":10730}]}}`,
			wantErr: `answered for pane "w6:p4"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := func(_ context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
				if socket != "/fixture/herdr.sock" || len(lines) != 1 ||
					!strings.Contains(string(lines[0]), `"w6:p3"`) {
					t.Errorf("process_info request %s %q", socket, lines)
				}
				return []json.RawMessage{json.RawMessage(tc.reply)}, nil
			}
			got, err := bridgeForeground(call, func() uint64 { return 1 })(
				context.Background(), "/fixture/herdr.sock", "w6:p3")
			if tc.wantErr == "" {
				if err != nil || !slices.Equal(got, tc.want) {
					t.Fatalf("foreground = %v, %v; want %v", got, err, tc.want)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("foreground = %v, %v; want an error naming %q", got, err, tc.wantErr)
			}
		})
	}
}

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

// TestHerdrBridgeTmuxGivesUpOnAFrozenServer pins the bridge read's bound: a
// pfm tmux server that accepts and never answers fails the proof within
// gather.ProbeTimeout, so it never stalls the painter that asked.
func TestHerdrBridgeTmuxGivesUpOnAFrozenServer(t *testing.T) {
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
	done := make(chan error, 1)
	go func() {
		_, err := bridgeTmux(context.Background(), socketPath, "list-clients", "-F", "#{client_pid}")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, gather.ErrServerUnresponsive) {
			t.Fatalf(
				"read of a frozen server = %v, want %v within %s",
				err,
				gather.ErrServerUnresponsive,
				gather.ProbeTimeout,
			)
		}
	case <-time.After(gather.ProbeTimeout + 5*time.Second):
		t.Fatalf("read of a frozen server still waiting after %s", gather.ProbeTimeout+5*time.Second)
	}
}
