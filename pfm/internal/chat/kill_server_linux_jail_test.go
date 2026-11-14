//go:build jail && linux

package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestNativeSelfEndWorkerSurvivesPTYSessionDeath(t *testing.T) {
	for _, unbound := range []bool{false, true} {
		t.Run(fmt.Sprintf("unbound-%t", unbound), func(t *testing.T) {
			testjail.DatabaseFiles(t, testjail.Fleet(t))
			caller := filepath.Join(t.TempDir(), "caller.json")
			shadow := t.TempDir()
			if err := testjail.WriteExecutable(
				filepath.Join(shadow, "shadow-pfm"),
				[]byte("#!/bin/sh\necho shadow worker refused >&2\nexit 23\n"),
				0o700,
			); err != nil {
				t.Fatal(err)
			}
			killed := make(chan bool, 1)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				if value, code, ok := nativeChatProof(req, "w6:p3"); ok {
					return value, code
				}
				if req.Method != "pane.close" {
					return nil, "unexpected_method"
				}
				body, err := os.ReadFile(caller)
				if err != nil {
					t.Error(err)
					return nil, "bad_caller"
				}
				var ids []int
				if err := json.Unmarshal(body, &ids); err != nil || len(ids) != 2 {
					t.Errorf("caller %q err=%v", body, err)
					return nil, "bad_caller"
				}
				argv, err := gather.NewProcFS("").Cmdline(ids[0])
				sid, sidErr := unix.Getsid(ids[0])
				if err != nil || sidErr != nil || sid != ids[1] || !slices.Contains(argv, "--self-end-fixture") {
					t.Errorf("caller identity argv=%q err=%v sid=%d err=%v", argv, err, sid, sidErr)
					return nil, "bad_caller"
				}
				values, resolveErr := paths.Resolve()
				if resolveErr != nil {
					t.Error(resolveErr)
					return nil, "bad_paths"
				}
				state := fleetdb.OpenSharedState(context.Background(), values)
				current, found, readErr := state.Seat(context.Background(), "restore-seat")
				_ = state.Close()
				if readErr != nil || !found || !current.EndPending {
					t.Errorf("close before durable intent: %+v %v %v", current, found, readErr)
				}
				// Herdr tears down the pane's session BEFORE its success acknowledgement.
				if err := syscall.Kill(-ids[1], syscall.SIGKILL); err != nil {
					t.Error(err)
					return nil, "kill_failed"
				}
				killed <- true
				return map[string]any{"type": "ok"}, ""
			})
			runtime, db, seat := nativeChatSeat(t, socket, "live")
			seat = nativeChatHost(t, db, seat, true)
			if unbound {
				seat.SessionID = ""
				if err := db.PutSeat(context.Background(), seat); err != nil {
					t.Fatal(err)
				}
			}
			command, err := testjail.PTYCommand(os.Args[0], "--self-end-fixture")
			if err != nil {
				t.Fatal(err)
			}
			command.Env = append(os.Environ(), "PFM_END_FIXTURE=1", "PFM_END_CALLER="+caller, "PATH="+shadow)
			var output bytes.Buffer
			command.Stdout = &output
			command.Stderr = &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			select {
			case <-killed:
			case err := <-done:
				t.Fatalf("caller exited before close: %v %s", err, &output)
			case <-time.After(15 * time.Second):
				t.Fatal("pane close never arrived")
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("initiating PTY did not die")
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				_, found, err := db.Seat(context.Background(), seat.SeatID)
				if err != nil {
					t.Fatal(err)
				}
				if !found {
					if _, err := os.Stat(
						filepath.Join(filepath.Dir(runtime.Paths.StateDB), "seats", seat.SeatID+".json"),
					); !os.IsNotExist(
						err,
					) {
						t.Fatalf("recipe err=%v", err)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("independent end worker did not retire the killed caller's seat: %s", &output)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
