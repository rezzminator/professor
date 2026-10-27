//go:build linux

package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// The initiating helper owns a real PTY session. The same binary also executes
// the production detached argv before testing's flag parser sees it.
func init() {
	if os.Getenv("PFM_END_FIXTURE") != "1" {
		return
	}
	values, err := paths.Resolve()
	if err != nil {
		panic(err)
	}
	db := fleetdb.OpenSharedState(context.Background(), values)
	seat, found, err := db.Seat(context.Background(), "restore-seat")
	if err != nil || !found {
		panic(fmt.Sprintf("seat found=%v err=%v", found, err))
	}
	_ = db.Close()
	target := headless.Chat{
		Backend: "herdr",
		SeatID:  seat.SeatID,
		ID:      seat.SessionID,
		Socket:  seat.HerdrSocket,
		Pane:    seat.HerdrPane,
	}
	if len(os.Args) > 3 && os.Args[len(os.Args)-2] == "--native-end-worker" {
		if err := endHerdr(context.Background(), target, values); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	tty, err := os.Open("/dev/tty")
	if err != nil {
		panic(err)
	}
	_ = tty.Close()
	sid, err := unix.Getsid(0)
	if err != nil {
		panic(err)
	}
	body, _ := json.Marshal([]int{os.Getpid(), sid})
	if err := os.WriteFile(os.Getenv("PFM_END_CALLER"), body, 0o600); err != nil {
		panic(err)
	}
	os.Args[0] = "shadow-pfm" // argv0 is not the executable image.
	code := EndResolved(context.Background(), target, os.Stdout, os.Stderr, &config.Runtime{Paths: values})
	if code != 0 {
		os.Exit(code)
	}
	// Keep the initiating session alive until Herdr closes it, even when scheduling returns.
	for {
		time.Sleep(time.Second)
	}
}

func TestNativeSelfEndWorkerSurvivesPTYSessionDeath(t *testing.T) {
	for _, unbound := range []bool{false, true} {
		t.Run(fmt.Sprintf("unbound-%t", unbound), func(t *testing.T) {
			testjail.Fleet(t)
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
			command := testjail.PTYCommand(os.Args[0], "--self-end-fixture")
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

func TestScheduleHerdrEndRejectsUnsafeLog(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "directory-symlink", "ancestor-symlink", "writable-directory", "trusted-directory-symlink"} {
		t.Run(kind, func(t *testing.T) {
			testjail.Fleet(t)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				value, code, _ := nativeChatProof(req, "w6:p3")
				return value, code
			})
			runtime, db, seat := nativeChatSeat(t, socket, "live")
			seat = nativeChatHost(t, db, seat, true)
			root := t.TempDir()
			dir := filepath.Join(root, "logs")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			leaf := filepath.Join(dir, "end-"+seat.SeatID+".log")
			switch kind {
			case "symlink":
				target := filepath.Join(root, "target")
				if err := os.WriteFile(target, []byte("private"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, leaf); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(leaf, 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory-symlink", "trusted-directory-symlink":
				alias := filepath.Join(root, "alias")
				if err := os.Symlink(dir, alias); err != nil {
					t.Fatal(err)
				}
				dir = alias
			case "ancestor-symlink":
				alias := filepath.Join(root, "alias")
				if err := os.Symlink(root, alias); err != nil {
					t.Fatal(err)
				}
				dir = filepath.Join(alias, "logs")
			case "writable-directory":
				if err := os.Chmod(dir, 0o777); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "directory-symlink" || kind == "ancestor-symlink" {
				if err := os.Chmod(root, 0o777); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "trusted-directory-symlink" {
				log, err := openEndWorkerLog(dir, "trusted.log")
				if err != nil {
					t.Fatalf("trusted system-style alias refused: %v", err)
				}
				if err := log.Close(); err != nil {
					t.Fatal(err)
				}
				return
			}
			runtime.Paths.SIDDir = dir
			done := make(chan error, 1)
			go func() {
				done <- ScheduleHerdrEnd(context.Background(), headless.Chat{ID: seat.SessionID, SeatID: seat.SeatID}, runtime)
			}()
			var err error
			select {
			case err = <-done:
			case <-time.After(time.Second):
				t.Error("end worker log open blocked on FIFO while holding conversation lock")
				// Unstick the unfixed writer so this regression fails on an assertion, not a suite timeout.
				reader, openErr := os.OpenFile(leaf, os.O_RDWR|unix.O_NONBLOCK, 0)
				if openErr != nil {
					t.Fatal(openErr)
				}
				defer func() { _ = reader.Close() }()
				select {
				case err = <-done:
				case <-time.After(time.Second):
					t.Fatal("blocked schedule did not leave after FIFO reader")
				}
			}
			if err == nil || !strings.Contains(err.Error(), "end worker log") {
				t.Errorf("unsafe %s accepted: %v", kind, err)
			}
			got, found, readErr := db.Seat(context.Background(), seat.SeatID)
			if readErr != nil || !found || got.EndPending {
				t.Fatalf("durable intent=%+v found=%v err=%v", got, found, readErr)
			}
			if _, readErr := db.ReadSeatRun(seat.SeatID); readErr != nil {
				t.Fatal(readErr)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			unlock, lockErr := fleetdb.LockSeat(ctx, runtime.Paths, seat.SessionID)
			if lockErr != nil {
				t.Fatal(errors.New("log refusal retained conversation lock: " + lockErr.Error()))
			}
			if err := unlock(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
