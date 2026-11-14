//go:build linux

package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
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
