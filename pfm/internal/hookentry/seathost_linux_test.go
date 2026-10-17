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
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

type seatFixtureReport struct {
	Argv0                             string
	PID, Group, Foreground            int
	Pane, Socket, Session             string
	SupervisorPID                     int
	LedgerMarker, LedgerPID, ArmCalls string
	LedgerArmed                       bool
	StdoutTTY, StderrTTY              bool
	Devices                           [3]uint64
}

// The re-exec fixture handles canonical argv0 before testing parses engine flags.
func init() {
	if os.Getenv("PFM_S3_FIXTURE") != "1" {
		return
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
	if strings.HasPrefix(os.Getenv("PFM_S3_MODE"), "reload-") {
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
				Home:    root,
				StateDB: os.Getenv(paths.EnvStateDB),
				SIDDir:  filepath.Join(root, "sid"),
				TmuxDir: filepath.Join(root, "tmux"),
			},
		}
		if os.Getenv("PFM_S3_MODE") == "launch" || strings.HasPrefix(os.Getenv("PFM_S3_MODE"), "launch-ledger-") {
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
		os.Exit(SeatHost([]string{"native-seat"}, os.Stdout, os.Stderr, runtime))
	}
	for _, mode := range []string{
		"exit", "ordinary-stdio", "redirected-stdin", "hup", "launch", "failed-missing-recipe", "failed-malformed-recipe", "failed-missing-executable",
		"ledger-on", "ledger-off", "launch-ledger-on", "launch-ledger-off",
		"reload-success", "reload-retry", "reload-close", "raw-codex", "raw-opencode",
	} {
		t.Run(mode, func(t *testing.T) {
			root, environment := testjail.FleetEnv(t)
			binary := filepath.Join(root, "2.1.302")
			if err := os.Symlink(os.Args[0], binary); err != nil {
				t.Fatal(err)
			}
			server := newSeatHostHerdr(t)
			server.addPane("w6:p3", "native")
			environment = append(
				environment,
				"PFM_S3_FIXTURE=1",
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
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSeatHostPTY$")
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
			if strings.HasPrefix(mode, "failed-") {
				if _, err := master.Write([]byte{4}); err != nil {
					t.Fatal(err)
				}
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
					if next.Pane != "w6:p3" || next.SupervisorPID != command.Process.Pid {
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

// This supervisor fixture implements only its two producer methods. The older
// bridge fixture has no native pane.get contract.
func newSeatHostHerdr(t *testing.T) *fakeHerdr {
	t.Helper()
	server := &fakeHerdr{path: filepath.Join(testjail.ShortRoot(t), "h.sock")}
	listener, err := net.Listen("unix", server.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var req struct {
					ID     string            `json:"id"`
					Method string            `json:"method"`
					Params map[string]string `json:"params"`
				}
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				line, _ := json.Marshal(req)
				server.mu.Lock()
				server.lines = append(server.lines, string(line))
				server.mu.Unlock()
				result := map[string]any{"type": "ok"}
				if req.Method == "pane.get" {
					result = map[string]any{
						"type": "pane_info",
						"pane": map[string]string{"pane_id": req.Params["pane_id"]},
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
