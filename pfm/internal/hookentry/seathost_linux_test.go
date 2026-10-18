//go:build linux

package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

type seatFixtureReport struct {
	Argv0                             string
	PID, Group, Foreground            int
	Pane, Socket, Session             string
	SupervisorPID                     int
	LedgerMarker, LedgerPID, ArmCalls string
	LedgerArmed                       bool
	HostStart                         uint64
	HostBirth                         int64
	Terminal                          string
	StdoutTTY, StderrTTY              bool
	Devices                           [3]uint64
}

// The re-exec fixture handles canonical argv0 before testing parses engine flags.
func init() {
	if os.Getenv("PFM_S3_FIXTURE") != "1" {
		return
	}
	if len(os.Args) >= 4 && os.Args[1] == "internal" && os.Args[2] == "seat-host" {
		TestSeatHostPTY(nil)
		return
	}
	if len(os.Args) >= 5 && os.Args[1] == "chat" && os.Args[2] == "end" && os.Args[3] == "--native-end-worker" {
		values, err := paths.Resolve()
		if err != nil {
			panic(err)
		}
		if err := pfmchat.EndNativeWorker(context.Background(), os.Args[4], values); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Args[0] != "claude" && os.Args[0] != pfmengine.MustLookup(pfmengine.Codex).Binary &&
		os.Args[0] != pfmengine.MustLookup(pfmengine.OpenCode).Binary &&
		os.Args[0] != "seat-descendant" {
		return
	}
	root := os.Getenv("PFM_S3_ROOT")
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	if os.Args[0] == "seat-descendant" {
		if err := os.WriteFile(filepath.Join(root, "descendant-ready"), []byte("ready"), 0o600); err != nil {
			panic(err)
		}
		<-hup
		if err := os.WriteFile(filepath.Join(root, "descendant-hup"), []byte("HUP"), 0o600); err != nil {
			panic(err)
		}
		os.Exit(0)
	}
	var child *exec.Cmd
	if os.Getenv("PFM_S3_MODE") == "hup" {
		child = exec.Command(os.Getenv("PFM_S3_BINARY"))
		child.Args = []string{"seat-descendant"}
		child.Env = os.Environ()
		if err := child.Start(); err != nil {
			panic(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(root, "descendant-ready")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				panic("descendant not ready")
			}
			time.Sleep(time.Millisecond)
		}
	}
	foreground, err := unix.IoctlGetInt(0, unix.TIOCGPGRP)
	if err != nil {
		panic(err)
	}
	_, stdoutErr := unix.IoctlGetTermios(1, unix.TCGETS)
	_, stderrErr := unix.IoctlGetTermios(2, unix.TCGETS)
	report := seatFixtureReport{
		StdoutTTY:     stdoutErr == nil,
		StderrTTY:     stderrErr == nil,
		Argv0:         os.Args[0],
		PID:           os.Getpid(),
		Group:         syscall.Getpgrp(),
		Foreground:    foreground,
		Pane:          os.Getenv("HERDR_PANE_ID"),
		Socket:        os.Getenv("HERDR_SOCKET_PATH"),
		SupervisorPID: os.Getppid(),
		LedgerMarker:  os.Getenv(claudelaunch.EnvCostLedger),
		LedgerPID:     os.Getenv(claudelaunch.EnvCostLedgerPID),
		ArmCalls:      os.Getenv("PFM_S3_ARM_CALLS"),
		LedgerArmed:   claudelaunch.CostArmedFor(os.Environ(), os.Getppid()),
	}
	if os.Getenv("PFM_S3_MODE") == "launch" || strings.HasPrefix(os.Getenv("PFM_S3_MODE"), "launch-ledger-") {
		db := fleetdb.OpenSharedState(context.Background(), paths.Values{StateDB: os.Getenv(paths.EnvStateDB)})
		seat, found, err := db.SeatByPane(context.Background(), report.Socket, report.Pane)
		if err != nil || !found {
			panic(fmt.Sprintf("launched seat found=%v err=%v", found, err))
		}
		report.Session = seat.SessionID
		if err := db.Close(); err != nil {
			panic(err)
		}
	}
	db := fleetdb.OpenSharedState(context.Background(), paths.Values{StateDB: os.Getenv(paths.EnvStateDB)})
	bound, found, readErr := db.Seat(context.Background(), "native-seat")
	if os.Getenv("PFM_S3_MODE") == "launch" || strings.HasPrefix(os.Getenv("PFM_S3_MODE"), "launch-ledger-") {
		bound, found, readErr = db.SeatByPane(context.Background(), report.Socket, report.Pane)
	}
	if readErr != nil || !found {
		panic(fmt.Sprintf("bound seat missing: %v %v", found, readErr))
	}
	_ = db.Close()
	report.HostStart = bound.SeatHostStart
	report.HostBirth = bound.SeatHostBirth
	report.Terminal = bound.HerdrTerminalID
	if bound.SeatHostPID != report.SupervisorPID {
		panic(fmt.Sprintf("host PID %d expected supervisor %d", bound.SeatHostPID, report.SupervisorPID))
	}
	if report.HostStart == 0 || report.HostBirth == 0 || report.Terminal == "" {
		panic(fmt.Sprintf("unstamped supervisor: %+v", bound))
	}
	for fd := range report.Devices {
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil {
			panic(err)
		}
		report.Devices[fd] = stat.Rdev
	}
	body, err := json.Marshal(report)
	if err != nil {
		panic(err)
	}
	reportFile := "engine-report"
	if os.Getenv("PFM_S3_PASS") == "next" {
		reportFile += "-next"
	}
	if err := atomicfile.Write(filepath.Join(root, reportFile), body, 0o600); err != nil {
		panic(err)
	}
	if strings.HasPrefix(os.Getenv("PFM_S3_MODE"), "reload-") || os.Getenv("PFM_S3_MODE") == "move-start" {
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(root, reportFile+"-release")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				panic("reload release timeout")
			}
			time.Sleep(time.Millisecond)
		}
	}
	if os.Getenv("PFM_S3_MODE") == "hangup-exit-one" {
		<-hup
		os.Exit(1)
	}
	if child != nil {
		<-hup
		if err := os.WriteFile(filepath.Join(root, "engine-hup"), []byte("HUP"), 0o600); err != nil {
			panic(err)
		}
		if err := child.Wait(); err != nil {
			panic(err)
		}
	}
	os.Exit(7)
}

func TestSeatHostPTY(t *testing.T) {
	if os.Getenv("PFM_S3_FIXTURE") == "1" {
		root := os.Getenv("PFM_S3_ROOT")
		mode := os.Getenv("PFM_S3_MODE")
		if strings.Contains(mode, "ledger-") {
			calls := 0
			armCostLedger = func(environ []string, _ paths.Env, enabled bool, _ io.Writer) ([]string, string) {
				calls++
				environ = seatEnvironment(
					environ,
					[]string{"PFM_S3_ARM_CALLS=" + strconv.Itoa(calls)},
					[]string{"PFM_S3_ARM_CALLS"},
				)
				values := []string{claudelaunch.EnvCostLedger + "=" + claudelaunch.CostLedgerOff}
				marker := claudelaunch.CostLedgerOff
				if enabled {
					values = claudelaunch.CostEnv(43123, os.Getpid())
					marker = claudelaunch.CostLedgerOn
				}
				return seatEnvironment(environ, values, claudelaunch.CostEnvNames()), marker
			}
		}
		runtime := config.Runtime{
			Config: config.Config{Claude: config.ClaudePrefs{CostLedger: strings.HasSuffix(mode, "ledger-on")}},
			Paths: paths.Values{
				Home:     root,
				StateDB:  os.Getenv(paths.EnvStateDB),
				ProcRoot: "/proc",
				SIDDir:   filepath.Join(root, "sid"),
				TmuxDir:  filepath.Join(root, "tmux"),
			},
		}
		if (os.Getenv("PFM_S3_MODE") == "launch" || strings.HasPrefix(os.Getenv("PFM_S3_MODE"), "launch-ledger-")) &&
			(len(os.Args) <= 2 || os.Args[1] != "internal") {
			os.Exit(
				Launch(
					[]string{"--real", os.Getenv("PFM_S3_BINARY"), "--cwd", root, "--"},
					os.Stdout,
					os.Stderr,
					runtime,
					paths.OSEnv{},
				),
			)
		}
		if mode == "redirected-stdin" {
			null, err := os.Open("/dev/null")
			if err != nil {
				t.Fatal(err)
			}
			if err := unix.Dup2(int(null.Fd()), 0); err != nil {
				t.Fatal(err)
			}
		}
		id := "native-seat"
		if len(os.Args) == 4 && os.Args[1] == "internal" {
			id = os.Args[3]
		}
		os.Exit(SeatHost([]string{id}, os.Stdout, os.Stderr, runtime))
	}
	for _, mode := range []string{
		"exit", "ordinary-stdio", "redirected-stdin", "hup", "launch", "failed-missing-recipe", "failed-malformed-recipe", "failed-missing-executable",
		"ledger-on", "ledger-off", "launch-ledger-on", "launch-ledger-off",
		"reload-failure-locked", "reload-success", "reload-retry", "reload-close", "reload-move-retry", "move-start", "raw-codex", "raw-opencode", "hangup-exit-one",
	} {
		t.Run(mode, func(t *testing.T) {
			root, environment := testjail.FleetEnv(t)
			binary := filepath.Join(root, "2.1.302")
			if err := os.Symlink(os.Args[0], binary); err != nil {
				t.Fatal(err)
			}
			server := newSeatHostHerdr(t, root, mode)
			server.addPane("w6:p3", "native")
			if mode == "move-start" {
				if err := os.WriteFile(filepath.Join(root, "move-on-proof"), []byte("move"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			environment = append(
				environment,
				"PFM_S3_FIXTURE=1",
				paths.EnvProcRoot+"=/proc",
				"PFM_S3_ROOT="+root,
				"PFM_S3_BINARY="+binary,
				"PFM_S3_MODE="+mode,
				"HERDR_ENV=1",
				"HERDR_PANE_ID=w6:p3",
				"HERDR_SOCKET_PATH="+server.path,
				"TMUX=",
				"CLAUDE_CONFIG_DIR=",
				paths.EnvStateDB+"="+filepath.Join(root, "pfm.db"),
			)
			values := paths.Values{StateDB: filepath.Join(root, "pfm.db")}
			db := fleetdb.OpenSharedState(context.Background(), values)
			defer func() { _ = db.Close() }()
			if strings.Contains(mode, "ledger-") {
				environment = append(environment, claudelaunch.CostEnv(11111, 1)...)
			}
			engineID := pfmengine.Claude
			if mode == "raw-codex" {
				engineID = pfmengine.Codex
			}
			if mode == "raw-opencode" {
				engineID = pfmengine.OpenCode
			}
			if mode != "launch" && !strings.HasPrefix(mode, "launch-ledger-") {
				if err := db.PutSeat(
					context.Background(),
					fleetdb.Seat{
						SeatID:      "native-seat",
						SessionID:   "fixture-session",
						Engine:      string(engineID),
						Backend:     "herdr",
						State:       "live",
						HerdrPane:   "w6:p3",
						HerdrSocket: server.path,
					},
				); err != nil {
					t.Fatal(err)
				}
				saved := append([]string(nil), environment...)
				saved = append(saved, "HERDR_PANE_ID=w1:p9", "HERDR_SOCKET_PATH=/stale/socket")
				run := fleetdb.SeatRun{
					Binary:     binary,
					Env:        saved,
					CWD:        root,
					CostLedger: strings.HasSuffix(mode, "ledger-on"),
				}
				if mode == "failed-malformed-recipe" {
					run.Binary = "relative-engine"
				}
				if mode == "failed-missing-executable" {
					run.Binary = filepath.Join(root, "missing-engine")
				}
				if mode != "failed-missing-recipe" {
					if err := db.WriteSeatRun("native-seat", run); err != nil {
						t.Fatal(err)
					}
				}
			}
			master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = master.Close() }()
			if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
				t.Fatal(err)
			}
			info, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", master.Fd()))
			_, index, found := strings.Cut(string(info), "tty-index:")
			if !found {
				t.Fatalf("pty has no tty-index: %q", info)
			}
			number := strings.TrimSpace(strings.SplitN(index, "\n", 2)[0])
			if err != nil {
				t.Fatal(err)
			}
			slave, err := os.OpenFile("/dev/pts/"+number, os.O_RDWR|syscall.O_NOCTTY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = slave.Close() }()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			hostBinary := filepath.Join(root, "pfm")
			if err := os.Symlink(os.Args[0], hostBinary); err != nil {
				t.Fatal(err)
			}
			args := []string{"internal", "seat-host", "native-seat"}
			if mode == "launch" || strings.HasPrefix(mode, "launch-ledger-") {
				args = []string{"-test.run=^TestSeatHostPTY$"}
			}
			command := exec.CommandContext(ctx, hostBinary, args...)
			command.Env = environment
			command.Stdin = slave
			command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
			var output bytes.Buffer
			command.Stdout = &output
			command.Stderr = &output
			if mode == "ordinary-stdio" {
				command.Stdout, command.Stderr = slave, slave
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			if mode == "move-start" {
				waitSeatFixture(t, done, func() bool {
					current, found, err := db.Seat(context.Background(), "native-seat")
					return err == nil && found && current.State == "live" && current.HerdrPane == "w9:p2"
				})
				if err := os.WriteFile(
					filepath.Join(root, "engine-report-release"),
					[]byte("ready"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(mode, "failed-") {
				if _, err := master.Write([]byte{4}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "reload-failure-locked" {
				waitSeatFixture(
					t,
					done,
					func() bool { _, err := os.Stat(filepath.Join(root, "engine-report")); return err == nil },
				)
				// Run the real reloader against a real supervisor. Its next exec fails.
				t.Setenv(paths.EnvStateDB, values.StateDB)
				t.Setenv(paths.EnvProcRoot, "/proc")
				request, options := seatReloadFailure(t, root, server.path)
				_, reloadErr := reload.RunHerdr(ctx, request, options, io.Discard)
				// Stop the helper before reading its shared output buffer on either path.
				cancel()
				<-done
				current, found, readErr := db.Seat(context.Background(), "native-seat")
				if reloadErr == nil || !strings.Contains(reloadErr.Error(), "new engine failed") ||
					!strings.Contains(reloadErr.Error(), "missing-next-engine") {
					t.Fatalf(
						"reload did not observe startup failure while holding exclusion: err=%v row=%+v output=%s",
						reloadErr,
						current,
						&output,
					)
				}
				if readErr != nil || !found || current.State != "failed" || !current.ReloadPending ||
					!strings.Contains(current.LostNote, "missing-next-engine") {
					t.Fatalf("failure publication=%+v found=%v err=%v output=%s", current, found, readErr, &output)
				}
				if _, err := db.ReadSeatRun(current.SeatID); err != nil {
					t.Fatal(err)
				}
				return
			}
			if strings.HasPrefix(mode, "reload-") {
				waitSeatFixture(
					t,
					done,
					func() bool { _, err := os.Stat(filepath.Join(root, "engine-report")); return err == nil },
				)
				seat, found, err := db.Seat(context.Background(), "native-seat")
				if err != nil || !found {
					t.Fatalf("read first seat: %v %v", found, err)
				}
				before := seat
				seat.ReloadPending = true
				run, err := db.ReadSeatRun(seat.SeatID)
				if err != nil {
					t.Fatal(err)
				}
				run.Args = []string{"--model", "next-model"}
				run.Env = append(run.Env, "PFM_S3_PASS=next")
				if mode != "reload-success" {
					run.Binary = filepath.Join(root, "missing-next-engine")
				}
				if err := db.WriteSeatRun(seat.SeatID, run); err != nil {
					t.Fatal(err)
				}
				if err := db.UpdateSeat(context.Background(), before, seat); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					filepath.Join(root, "engine-report-release"),
					[]byte("ready"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
				if mode != "reload-success" {
					waitSeatFixture(t, done, func() bool {
						current, found, err := db.Seat(context.Background(), seat.SeatID)
						return err == nil && found && current.State == "failed"
					})
					current, _, err := db.Seat(context.Background(), seat.SeatID)
					if err != nil || !current.ReloadPending || current.HerdrPane != "w6:p3" {
						t.Fatalf("failed reload lost pending/current pane: %+v err=%v", current, err)
					}
					if mode == "reload-move-retry" {
						before := current
						current.HerdrPane = "w9:p2"
						current.HerdrWorkspace = "w9"
						if err := os.WriteFile(
							filepath.Join(root, "canonical-pane"),
							[]byte(current.HerdrPane),
							0o600,
						); err != nil {
							t.Fatal(err)
						}
						if err := db.UpdateSeat(context.Background(), before, current); err != nil {
							t.Fatal(err)
						}
					}
					if mode == "reload-close" {
						if _, err := master.WriteString("q\n"); err != nil {
							t.Fatal(err)
						}
					} else {
						run.Binary = binary
						if err := db.WriteSeatRun(seat.SeatID, run); err != nil {
							t.Fatal(err)
						}
						if _, err := master.WriteString("\n"); err != nil {
							t.Fatal(err)
						}
					}
				}
				if mode != "reload-close" {
					waitSeatFixture(t, done, func() bool {
						current, found, err := db.Seat(context.Background(), seat.SeatID)
						_, fileErr := os.Stat(filepath.Join(root, "engine-report-next"))
						return err == nil && found && current.State == "live" && !current.ReloadPending &&
							fileErr == nil
					})
					body, err := os.ReadFile(filepath.Join(root, "engine-report-next"))
					if err != nil {
						t.Fatal(err)
					}
					var next seatFixtureReport
					if err := json.Unmarshal(body, &next); err != nil {
						t.Fatal(err)
					}
					wantPane := "w6:p3"
					if mode == "reload-move-retry" {
						wantPane = "w9:p2"
					}
					if next.Pane != wantPane || next.SupervisorPID != command.Process.Pid {
						t.Fatalf("reload moved lifetime owner: %+v", next)
					}
					if err := os.WriteFile(
						filepath.Join(root, "engine-report-next-release"),
						[]byte("ready"),
						0o600,
					); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "hup" {
				deadline := time.NewTimer(8 * time.Second)
				defer deadline.Stop()
				tick := time.NewTicker(time.Millisecond)
				defer tick.Stop()
			ready:
				for {
					select {
					case err := <-done:
						t.Fatalf("supervisor exited before engine readiness: %v %s", err, &output)
					case <-deadline.C:
						_ = command.Process.Signal(syscall.SIGHUP)
						t.Fatal("engine readiness timeout")
					case <-tick.C:
						if _, err := os.Stat(filepath.Join(root, "engine-report")); err == nil {
							break ready
						}
					}
				}
				if err := command.Process.Signal(syscall.SIGHUP); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "hangup-exit-one" {
				waitSeatFixture(t, done, func() bool {
					_, err := os.Stat(filepath.Join(root, "engine-report"))
					return err == nil
				})
				if err := master.Close(); err != nil {
					t.Fatal(err)
				}
				<-done
				if command.ProcessState.ExitCode() != 129 {
					t.Fatalf("terminal loss status=%d output=%s", command.ProcessState.ExitCode(), &output)
				}
				if _, found, err := db.Seat(context.Background(), "native-seat"); err != nil || !found {
					t.Fatalf("terminal loss discarded seat: found=%t err=%v", found, err)
				}
				if _, err := db.ReadSeatRun("native-seat"); err != nil {
					t.Fatalf("terminal loss discarded recipe: %v", err)
				}
				return
			}
			err = <-done
			if strings.HasPrefix(mode, "failed-") {
				cause := map[string]string{
					"failed-missing-recipe":     "read seat run",
					"failed-malformed-recipe":   "binary and cwd must be absolute",
					"failed-missing-executable": "start foreground engine",
				}[mode]
				if command.ProcessState.ExitCode() != 1 || !strings.Contains(output.String(), cause) {
					t.Fatalf("failed startup status=%v output=%s", err, &output)
				}
				seat, found, err := db.Seat(context.Background(), "native-seat")
				if err != nil || !found || seat.State != "failed" || !strings.Contains(seat.LostNote, cause) {
					t.Fatalf("failed startup seat=%+v found=%v err=%v output=%s", seat, found, err, &output)
				}
				if mode != "failed-missing-recipe" {
					run, err := db.ReadSeatRun(seat.SeatID)
					if err != nil || run.Binary == "" || run.CWD != root {
						t.Fatalf("failed startup recipe=%+v err=%v", run, err)
					}
				}
				return
			}
			if mode == "reload-close" {
				if command.ProcessState.ExitCode() != 0 ||
					!strings.Contains(output.String(), "press Enter to retry, q to close") {
					t.Fatalf("close retry status=%v output=%s", err, &output)
				}
				seats, readErr := db.Seats(context.Background())
				if readErr != nil || len(seats) != 0 {
					t.Fatalf("closed failed seats=%+v err=%v", seats, readErr)
				}
				return
			}
			if mode == "reload-retry" && !strings.Contains(output.String(), "press Enter to retry, q to close") {
				t.Fatalf("missing retry UX: %s", &output)
			}
			if command.ProcessState.ExitCode() != 7 {
				t.Fatalf("supervisor status=%v: %s", err, &output)
			}
			body, err := os.ReadFile(filepath.Join(root, "engine-report"))
			if err != nil {
				t.Fatal(err)
			}
			var report seatFixtureReport
			if err := json.Unmarshal(body, &report); err != nil {
				t.Fatal(err)
			}
			var terminal unix.Stat_t
			if err := unix.Fstat(int(slave.Fd()), &terminal); err != nil {
				t.Fatal(err)
			}
			for fd, device := range report.Devices {
				if device != terminal.Rdev {
					t.Errorf("engine fd%d device=%d, concrete PTY device=%d", fd, device, terminal.Rdev)
				}
			}
			if report.Argv0 != pfmengine.MustLookup(engineID).Binary || report.Group != report.PID ||
				report.Foreground != report.Group ||
				report.Pane != "w6:p3" ||
				report.Socket != server.path ||
				!report.StdoutTTY ||
				!report.StderrTTY {
				t.Fatalf("foreground engine = %+v", report)
			}
			if (mode == "launch" || strings.HasPrefix(mode, "launch-ledger-")) && report.Session == "" {
				t.Fatalf("launcher did not persist session: %+v", report)
			}
			if strings.Contains(mode, "ledger-") {
				enabled := strings.HasSuffix(mode, "ledger-on")
				marker := claudelaunch.CostLedgerOff
				if enabled {
					marker = claudelaunch.CostLedgerOn
				}
				if report.LedgerMarker != marker || report.LedgerArmed != enabled || report.ArmCalls != "1" ||
					report.SupervisorPID != command.Process.Pid ||
					(enabled && report.LedgerPID != strconv.Itoa(command.Process.Pid)) ||
					(!enabled && report.LedgerPID != "") {
					t.Fatalf("supervisor ledger policy enabled=%t: %+v", enabled, report)
				}
			}
			closed := false
			for _, line := range server.requests() {
				if strings.Contains(line, `"method":"pane.close"`) {
					closed = true
				}
			}
			if !closed {
				t.Fatalf("ended seat did not close shell-hosted pane: %v", server.requests())
			}
			seats, err := db.Seats(context.Background())
			if err != nil || len(seats) != 0 {
				t.Fatalf("ended seats=%+v err=%v", seats, err)
			}
			files, err := filepath.Glob(filepath.Join(root, "seats", "*.json"))
			if err != nil || len(files) != 0 {
				t.Fatalf("ended recipes=%v err=%v", files, err)
			}
			if mode == "hup" {
				for _, name := range []string{"engine-hup", "descendant-hup"} {
					body, err := os.ReadFile(filepath.Join(root, name))
					if err != nil || strings.TrimSpace(string(body)) != "HUP" {
						t.Fatalf("%s=%q err=%v", name, body, err)
					}
				}
			}
		})
	}
}

// This supervisor fixture supplies native identity, enumeration and shell proof.
// The older bridge fixture has no native pane.get contract.
func newSeatHostHerdr(t *testing.T, roots ...string) *fakeHerdr {
	t.Helper()
	server := &fakeHerdr{path: filepath.Join(testjail.ShortRoot(t), "h.sock")}
	listener, err := net.Listen("unix", server.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		proofReads := 0
		currentPane := "w6:p3"
		text := "❯ "
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var req struct {
					ID     string                     `json:"id"`
					Method string                     `json:"method"`
					Params map[string]json.RawMessage `json:"params"`
				}
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				var pane string
				_ = json.Unmarshal(req.Params["pane_id"], &pane)
				line, _ := json.Marshal(req)
				server.mu.Lock()
				server.lines = append(server.lines, string(line))
				server.mu.Unlock()
				result := map[string]any{"type": "ok"}
				if len(roots) > 0 {
					if raw, err := os.ReadFile(filepath.Join(roots[0], "canonical-pane")); err == nil {
						currentPane = string(raw)
					} else if !os.IsNotExist(err) {
						t.Error(err)
					}
				}
				if req.Method == "pane.get" {
					result = testjail.HerdrPaneInfo(pane)
					result["pane"].(map[string]any)["terminal_id"] = "stable-terminal"
				}
				if req.Method == "pane.list" {
					proofReads++
					if len(roots) > 0 {
						if _, err := os.Stat(filepath.Join(roots[0], "move-on-proof")); err == nil && proofReads >= 2 {
							currentPane = "w9:p2"
						}
					}
					pane := testjail.HerdrPaneInfo(currentPane)["pane"].(map[string]any)
					pane["terminal_id"] = "stable-terminal"
					result = map[string]any{"type": "pane_list", "panes": []any{pane}}
				}
				if req.Method == "pane.process_info" {
					shell := 0
					proc := gather.NewProcFS("/proc")
					pids, err := proc.PIDs()
					if err != nil {
						t.Error(err)
					}
					for _, pid := range pids {
						argv, err := proc.Cmdline(pid)
						if err != nil {
							continue
						}
						if len(argv) != 4 || argv[1] != "internal" || argv[2] != "seat-host" {
							continue
						}
						env, err := proc.Environ(pid)
						if err != nil {
							continue
						}
						if len(roots) > 0 && env["PFM_S3_ROOT"] != roots[0] {
							continue
						}
						shell = pid
						break
					}
					result = map[string]any{
						"type":         "pane_process_info",
						"process_info": map[string]any{"pane_id": pane, "shell_pid": shell},
					}
				}

				if len(roots) > 1 && roots[1] == "reload-failure-locked" {
					switch req.Method {
					case "pane.process_info":
						raw, err := os.ReadFile(filepath.Join(roots[0], "engine-report"))
						if err == nil {
							var report seatFixtureReport
							if err := json.Unmarshal(raw, &report); err != nil {
								t.Error(err)
							}
							result["process_info"].(map[string]any)["foreground_processes"] = []any{
								map[string]any{
									"pid":   report.PID,
									"name":  "claude",
									"argv0": "claude",
									"argv":  []string{"claude"},
								},
							}
						} else if !os.IsNotExist(err) {
							t.Error(err)
						}
					case "agent.get":
						result = map[string]any{
							"type": "agent_info",
							"agent": map[string]any{
								"pane_id":          currentPane,
								"terminal_id":      "stable-terminal",
								"agent_status":     "idle",
								"workspace_id":     strings.Split(currentPane, ":")[0],
								"tab_id":           strings.Split(currentPane, ":")[0] + ":t1",
								"focused":          true,
								"state_change_seq": 0,
								"revision":         1,
							},
						}
					case "pane.read":
						result = map[string]any{
							"type": "pane_read",
							"read": map[string]any{
								"pane_id":      currentPane,
								"workspace_id": "w6",
								"text":         text,
								"truncated":    false,
								"format":       "ansi",
							},
						}
					case "pane.send_text":
						var value string
						if err := json.Unmarshal(req.Params["text"], &value); err != nil {
							t.Error(err)
						}
						text += value
					case "pane.send_keys":
						var keys []string
						if err := json.Unmarshal(req.Params["keys"], &keys); err != nil {
							t.Error(err)
						}
						for _, key := range keys {
							if key == "Enter" {
								if err := os.WriteFile(
									filepath.Join(roots[0], "engine-report-release"),
									[]byte("ready"),
									0o600,
								); err != nil {
									t.Error(err)
								}
							}
						}
					}
				}

				_ = json.NewEncoder(conn).Encode(map[string]any{"id": req.ID, "result": result})
			}()
		}
	}()
	return server
}

func waitSeatFixture(t *testing.T, done <-chan error, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("supervisor exited before expected transition: %v", err)
		case <-deadline.C:
			t.Fatal("supervisor transition timeout")
		case <-tick.C:
			if ready() {
				return
			}
		}
	}
}

func TestSeatHostHangupPreservesRestorableSeat(t *testing.T) {
	ctx := context.Background()
	values := paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")}
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	seat := fleetdb.Seat{
		SeatID: "interrupted", SessionID: "session", Backend: "herdr", State: "live",
		HerdrSocket: "/fixture/stopped.sock", HerdrPane: "w6:p3",
	}
	if err := db.PutSeat(ctx, seat); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteSeatRun(seat.SeatID, fleetdb.SeatRun{Binary: "/fixture/engine"}); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if status := closeSeat(ctx, db, seat, 129, &stderr); status != 129 || stderr.Len() != 0 {
		t.Fatalf("hangup status=%d stderr=%s", status, &stderr)
	}
	if _, found, err := db.Seat(ctx, seat.SeatID); err != nil || !found {
		t.Fatalf("hangup discarded restoration owner: found=%t err=%v", found, err)
	}
	if _, err := db.ReadSeatRun(seat.SeatID); err != nil {
		t.Fatalf("hangup discarded restoration recipe: %v", err)
	}
}

func TestSeatHostDoesNotCloseAnAbsentSeat(t *testing.T) {
	values := paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")}
	db := fleetdb.OpenSharedState(context.Background(), values)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	var stderr bytes.Buffer
	status := closeSeat(
		context.Background(),
		db,
		fleetdb.Seat{SeatID: "removed", HerdrSocket: "/fixture/unowned.sock", HerdrPane: "w6:p3"},
		0,
		&stderr,
	)
	if status != 0 || stderr.Len() != 0 {
		t.Fatalf("absent seat attempted stale close: status=%d stderr=%s", status, &stderr)
	}
}

func TestSeatHostCloseFailureKeepsOwnerAndRecipe(t *testing.T) {
	values := paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")}
	db := fleetdb.OpenSharedState(context.Background(), values)
	defer func() { _ = db.Close() }()
	seat := fleetdb.Seat{
		SeatID:      "pending-close",
		SessionID:   "session",
		Backend:     "herdr",
		State:       "live",
		HerdrSocket: "/fixture/missing.sock",
		HerdrPane:   "w1:p1",
	}
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteSeatRun(seat.SeatID, fleetdb.SeatRun{Binary: "/fixture/engine"}); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := closeSeat(context.Background(), db, seat, 0, &stderr, config.Runtime{Paths: values})
	_, found, err := db.Seat(context.Background(), seat.SeatID)
	_, recipeErr := db.ReadSeatRun(seat.SeatID)
	if code != 1 || !found || err != nil || recipeErr != nil ||
		!strings.Contains(stderr.String(), "prove native end") ||
		!strings.Contains(stderr.String(), "missing.sock") {
		t.Fatalf("failed close lost durable recovery: code=%d found=%v err=%v recipe=%v", code, found, err, recipeErr)
	}
}

func TestFailedSeatStartDoesNotOverwriteAReplacementSupervisor(t *testing.T) {
	values := paths.Values{StateDB: filepath.Join(t.TempDir(), "state.db")}
	db := fleetdb.OpenSharedState(context.Background(), values)
	defer func() { _ = db.Close() }()
	old := fleetdb.Seat{
		SeatID:        "rebound",
		Backend:       "herdr",
		State:         "live",
		SeatHostPID:   100,
		SeatHostStart: 1,
		SeatHostBirth: 1,
	}
	next := old
	next.SeatHostPID = 101
	next.SeatHostStart = 2
	next.SeatHostBirth = 2
	if err := db.PutSeat(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	failedSeatStart(context.Background(), db, old, fmt.Errorf("old startup failed"), &stderr)
	current, found, err := db.Seat(context.Background(), old.SeatID)
	if err != nil || !found || current != next || !strings.Contains(stderr.String(), "supervisor binding changed") {
		t.Fatalf(
			"replacement changed: before=%+v after=%+v found=%v err=%v stderr=%s",
			next,
			current,
			found,
			err,
			&stderr,
		)
	}
}
