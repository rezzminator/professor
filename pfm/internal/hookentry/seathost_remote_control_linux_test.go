//go:build linux

package hookentry

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func init() {
	if os.Getenv("PFM_REMOTE_HOST_CHILD") != "1" {
		return
	}
	runtime, err := config.LoadRuntime(os.Getenv(paths.EnvConfig))
	if err != nil {
		panic(err)
	}
	if os.Getenv("PFM_REMOTE_HOST_CORRUPT_CONFIG") == "1" {
		if err := os.WriteFile(runtime.Config.Path, []byte("{broken"), 0o600); err != nil {
			panic(err)
		}
	}
	// Retained supervisor preferences are stale; the file must win at exec.
	runtime.Config.Claude.RemoteControl = !runtime.Config.Claude.RemoteControl
	os.Exit(SeatHost([]string{"native-seat"}, os.Stdout, os.Stderr, runtime))
}

// Exercise the supervisor itself: old saved recipes can restart without ever
// passing through the restored-chat door.
func TestSeatHostRefreshesLegacyRemoteControl(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) { runRemoteControlSeatHost(t, enabled, false) })
	}
}

func TestSeatHostRemoteControlUnreadableConfigDoesNotBlock(t *testing.T) {
	runRemoteControlSeatHost(t, false, true)
}

func runRemoteControlSeatHost(t *testing.T, enabled, corrupt bool) {
	t.Helper()
	root, environment := testjail.FleetEnv(t)
	binary := filepath.Join(root, "fixture-engine")
	argsFile := filepath.Join(root, "engine-args")
	if err := testjail.WriteExecutable(
		binary,
		[]byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+argsFile+"'\nexit 129\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "pfm.config.json")
	if err := os.WriteFile(
		configPath,
		[]byte(
			fmt.Sprintf(
				`{"version":%d,"accounts":[],"claude":{"remoteControl":%t,"costLedger":false}}`,
				config.Version,
				enabled,
			),
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	server := newSeatHostHerdr(t, root, "exit")
	server.addPane("w6:p3", "native")
	state := filepath.Join(root, "state.db")
	db := fleetdb.OpenSharedState(context.Background(), paths.Values{StateDB: state})
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.PutSeat(
		context.Background(),
		fleetdb.Seat{
			SeatID:      "native-seat",
			SessionID:   "old-session",
			Name:        "desk",
			Engine:      string(pfmengine.Claude),
			Backend:     "herdr",
			State:       "live",
			HerdrPane:   "w6:p3",
			HerdrSocket: server.path,
		},
	); err != nil {
		t.Fatal(err)
	}
	args := []string{"--resume", "old-session"}
	if !enabled {
		args = append(args, "--remote-control", "stale name")
	}
	if err := db.WriteSeatRun(
		"native-seat",
		fleetdb.SeatRun{Binary: binary, CWD: root, Args: args},
	); err != nil {
		t.Fatal(err)
	}
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = master.Close() }()
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	info, err := os.ReadFile("/proc/self/fdinfo/" + strconv.Itoa(int(master.Fd())))
	if err != nil {
		t.Fatal(err)
	}
	_, index, found := strings.Cut(string(info), "tty-index:")
	if !found {
		t.Fatalf("pty has no tty-index: %q", info)
	}
	number := strings.TrimSpace(strings.SplitN(index, "\n", 2)[0])
	slave, err := os.OpenFile("/dev/pts/"+number, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slave.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	host := filepath.Join(root, "pfm")
	if err := os.Symlink(os.Args[0], host); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, host, "internal", "seat-host", "native-seat")
	environment = append(
		environment,
		"PFM_S3_ROOT="+root,
		"PFM_REMOTE_HOST_CHILD=1",
		paths.EnvConfig+"="+configPath,
		paths.EnvStateDB+"="+state,
		paths.EnvProcRoot+"=/proc",
		"HERDR_ENV=1",
		"HERDR_SOCKET_PATH="+server.path,
		"HERDR_PANE_ID=w6:p3",
		"TMUX=",
	)
	if corrupt {
		environment = append(environment, "PFM_REMOTE_HOST_CORRUPT_CONFIG=1")
	}
	command.Env = environment
	command.Stdin = slave
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil && command.ProcessState.ExitCode() != 129 {
		t.Fatalf("supervisor failed: %v: %s", err, output.String())
	}
	body, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if corrupt && (!strings.Contains(output.String(), "WARN") || !strings.Contains(output.String(), "desk")) {
		t.Fatalf("missing visible policy warning: %s", output.String())
	}
	args = strings.Split(strings.TrimSpace(string(body)), "\n")
	at := slices.Index(args, "--remote-control")
	if (at >= 0) != enabled || (enabled && (at+1 == len(args) || args[at+1] != "desk")) {
		t.Fatalf("supervisor restarted legacy recipe without named Remote Control: %q", args)
	}
}
