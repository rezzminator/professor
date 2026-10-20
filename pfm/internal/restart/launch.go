package restart

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

const (
	usage = "usage: pfm restart [--dry-run]"
	// WorkerCommand is the `pfm internal` entry of the detached worker a plain
	// `pfm restart` starts; no user types it.
	WorkerCommand = "restart-run"
	workerUsage   = "usage: pfm internal " + WorkerCommand +
		" --front PID [--launched-from SOCKET [--launched-pane PANE]]"
)

// Main is `pfm restart`. --dry-run prints the plan and changes nothing; a
// plain run hands the restart to a detached worker and follows its log, so
// the restart finishes even when the chat that typed it ends.
func Main(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	flags := cli.NewFlagSet("restart", usage, stderr)
	dryRun := flags.Bool("dry-run", false, "print the ordered plan and change nothing")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	ctx := context.Background()
	if *dryRun {
		dependencies := LiveDeps(runtime, stderr, nil, launchingChat())
		dependencies.DryRun = true
		plan := BuildPlan(ctx, dependencies)
		plan.Print(stdout)
		for _, finding := range plan.Findings {
			if finding.Err != nil {
				return 1
			}
		}
		return 0
	}
	return launchFront(ctx, stdout, stderr, runtime)
}

// sessionLeader reports whether this process leads its own session, as the
// worker Spawn starts does; a seam so a test can play either.
var sessionLeader = func() bool {
	sid, err := unix.Getsid(0)
	return err == nil && sid == os.Getpid()
}

// Worker is `pfm internal restart-run`: the restart itself, run by the worker
// a plain `pfm restart` detaches. It refuses to run anywhere else — typed in
// a chat it would run in the foreground and die with that chat mid-restart.
func Worker(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	flags := cli.NewFlagSet("internal "+WorkerCommand, workerUsage, stderr)
	front := flags.Int("front", 0, "the pid of the pfm restart that launched this worker")
	socket := flags.String("launched-from", "", "the tmux socket name or Herdr socket path of the chat that typed it")
	pane := flags.String("launched-pane", "", "the Herdr pane of that chat")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || *front <= 0 || (*pane != "" && *socket == "") {
		flags.Usage()
		return 2
	}
	if !sessionLeader() {
		fmt.Fprintf(stderr, "pfm internal %s: runs only as the detached worker pfm restart starts "+
			"in its own session; type pfm restart\n", WorkerCommand)
		return 2
	}
	ctx := context.Background()
	launchedFrom := chatAddress{Socket: *socket, Pane: *pane}
	plan := BuildPlan(ctx, LiveDeps(runtime, stdout, []int{*front}, launchedFrom))
	plan.Print(stdout)
	return plan.Execute(ctx, stdout)
}

// chatAddress is where a chat lives: its tmux socket name, or, for a chat
// running directly in a Herdr pane, its Herdr socket path and pane.
type chatAddress struct {
	Socket string
	Pane   string
}

// holds reports whether chat is the chat at this address.
func (address chatAddress) holds(chat headless.Chat) bool {
	if address.Socket == "" || chat.Socket != address.Socket {
		return false
	}
	if address.Pane == "" {
		return chat.Backend != spawn.BackendHerdr
	}
	return chat.Backend == spawn.BackendHerdr && chat.Pane == address.Pane
}

// launchingChat is the address of the chat this process runs in, read through
// the same caller identity `pfm kill --self` resolves: a chat directly in a
// Herdr pane by its Herdr socket and pane, a tmux chat by its socket; zero
// outside every chat.
func launchingChat() chatAddress {
	self := kill.Environment(paths.OSEnv{})
	if self.Native() {
		return chatAddress{Socket: self.HerdrSocket, Pane: self.HerdrPane}
	}
	if self.TMUX == "" {
		return chatAddress{}
	}
	socket, _, _ := strings.Cut(self.TMUX, ",")
	return chatAddress{Socket: filepath.Base(socket)}
}

// restartWorkerEnv is the worker's environment: this one, minus every
// variable that ties a process to the chat pane it was typed in (the Herdr
// pane names and TMUX/TMUX_PANE). HERDR_BIN_PATH stays: it names the Herdr
// binary the worker stops and starts, not a pane.
func restartWorkerEnv(environ []string) []string {
	kept := herdr.StripReporter(environ)
	for _, entry := range environ {
		if strings.HasPrefix(entry, herdr.EnvBin+"=") {
			kept = append(kept, entry)
		}
	}
	return kept
}

// launchFront starts the detached worker and follows its log.
func launchFront(ctx context.Context, stdout, stderr io.Writer, runtime config.Runtime) int {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "pfm restart: resolve this binary: %v\n", err)
		return 1
	}
	argv := []string{executable}
	if runtime.Config.Path != "" {
		argv = append(argv, "--config", runtime.Config.Path)
	}
	argv = append(argv, "internal", WorkerCommand, "--front", strconv.Itoa(os.Getpid()))
	if launched := launchingChat(); launched.Socket != "" {
		argv = append(argv, "--launched-from", launched.Socket)
		if launched.Pane != "" {
			argv = append(argv, "--launched-pane", launched.Pane)
		}
	}
	logDir := restartLogDir(runtime.Paths)
	name := fmt.Sprintf("restart-%s-%d.log", clock.Real.Now().UTC().Format("20060102T150405Z"), os.Getpid())
	launcher := Launcher{Runner: obs.Runner(deps.RealRunner{}), Clock: clock.Real, Exited: childExited}
	pid, logPath, err := launcher.Spawn(ctx, argv, restartWorkerEnv(os.Environ()), logDir, name)
	if err != nil {
		fmt.Fprintf(stderr, "pfm restart: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "pfm restart: worker pid %d, detached; log %s\n", pid, logPath)
	return launcher.Follow(ctx, pid, logPath, stdout, stderr)
}

// restartLogDir is where restart logs and the logs of what it starts go:
// beside the state database, so a jail, a fence and the host keep their own.
func restartLogDir(values paths.Values) string {
	return filepath.Join(filepath.Dir(values.StateDB), "restart")
}

// Launcher spawns the worker outside every chat and follows its log.
type Launcher struct {
	Runner deps.Runner
	Clock  clock.Clock
	// Exited reports whether the worker pid has exited.
	Exited func(pid int) (bool, error)
}

// Spawn starts argv in its own session — no controlling terminal, so a chat's
// tmux server ending does not take it along — writing to a fresh log.
func (launcher Launcher) Spawn(ctx context.Context, argv, env []string, logDir, name string) (int, string, error) {
	log, logPath, err := openStartLog(logDir, name)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = log.Close() }() // the worker holds its own copy
	process, err := launcher.Runner.Start(ctx, argv, deps.StartOptions{
		Detach: true, Env: env, Stdout: log, Stderr: log,
	})
	if err != nil {
		return 0, "", fmt.Errorf("start the restart worker: %w", err)
	}
	return process.Pid(), logPath, nil
}

// Follow copies the worker's log to stdout until its exit marker, and returns
// the worker's exit code. A worker gone without its marker is a failure.
func (launcher Launcher) Follow(ctx context.Context, pid int, logPath string, stdout, stderr io.Writer) int {
	var offset int64
	var pending []byte
	for {
		exited, exitErr := launcher.Exited(pid)
		chunk, err := readFrom(logPath, offset)
		if err != nil {
			fmt.Fprintf(stderr, "pfm restart: follow %s: %v\n", logPath, err)
			return 1
		}
		offset += int64(len(chunk))
		pending = append(pending, chunk...)
		for {
			line, rest, found := bytes.Cut(pending, []byte("\n"))
			if !found {
				break
			}
			pending = rest
			_, _ = stdout.Write(append(line, '\n'))
			if code, ok := strings.CutPrefix(string(line), ExitMarker); ok {
				parsed, err := strconv.Atoi(strings.TrimSpace(code))
				if err != nil {
					return 1
				}
				return parsed
			}
		}
		if exitErr != nil {
			fmt.Fprintf(stderr, "pfm restart: worker pid %d: %v; log %s\n", pid, exitErr, logPath)
			return 1
		}
		if exited {
			fmt.Fprintf(stderr, "pfm restart: worker pid %d exited without its final line; log %s\n", pid, logPath)
			return 1
		}
		if err := launcher.Clock.Sleep(ctx, pollEvery); err != nil {
			fmt.Fprintf(stderr, "pfm restart: follow %s: %v\n", logPath, err)
			return 1
		}
	}
}

// readFrom reads path from offset to its current end.
func readFrom(path string, offset int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek: %w", err)
	}
	chunk, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return chunk, nil
}

// childExited reaps the released worker without blocking: it is still this
// process's child, so wait4 is the one signal that tells a finished worker
// from a running one (kill -0 answers yes for a zombie).
func childExited(pid int) (bool, error) {
	var status syscall.WaitStatus
	reaped, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	if errors.Is(err, syscall.ECHILD) {
		if killErr := syscall.Kill(pid, 0); errors.Is(killErr, syscall.ESRCH) {
			return true, nil
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("wait4: %w", err)
	}
	return reaped == pid, nil
}

// liveFleet is the real chat roster: the fleet scan, and chat.EndForRestart.
type liveFleet struct {
	runtime config.Runtime
	warn    io.Writer
}

func (fleet liveFleet) Live(ctx context.Context) ([]headless.Chat, map[string]string, error) {
	scan, err := pfmchat.ScanRows(ctx, fleet.warn, &fleet.runtime)
	if err != nil {
		return nil, nil, err
	}
	var chats []headless.Chat
	for index := range scan.Rows {
		row := scan.Rows[index]
		if row.Kind.IsLiveSeat() || row.Kind == compose.Booting {
			chats = append(chats, pfmchat.FromRow(row))
		}
	}
	return chats, scan.Unreadable, nil
}

func (fleet liveFleet) End(ctx context.Context, chat headless.Chat) error {
	return pfmchat.EndForRestart(ctx, chat, fleet.runtime.Paths)
}

// LiveDeps is the host's real doors for one restart.
func LiveDeps(runtime config.Runtime, warn io.Writer, spare []int, launchedFrom chatAddress) Deps {
	runner := obs.Runner(deps.RealRunner{})
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(warn, "pfm restart: resolve this binary (pfm processes are matched by name only): %v\n", err)
	}
	dependencies := Deps{
		Runner:       runner,
		Procs:        gather.NewProcFS(runtime.Paths.ProcRoot),
		Signal:       syscall.Kill,
		Clock:        clock.Real,
		Fleet:        liveFleet{runtime: runtime, warn: warn},
		Services:     nativeServiceManager(goruntime.GOOS, runner, clock.Real, runtime.Paths.Home, os.Getuid()),
		HTTPProbe:    probeURL,
		Self:         os.Getpid(),
		EffectiveUID: uint32(os.Geteuid()),
		Spare:        spare,
		Executable:   executable,
		ConfigPath:   runtime.Config.Path,
		HerdrBinary:  (paths.OSEnv{}).Get(herdr.EnvBin),
		LaunchedFrom: launchedFrom,
		LogDir:       restartLogDir(runtime.Paths),
		Env:          restartWorkerEnv(os.Environ()),
	}
	if runtime.Config.Harvester.Enabled && runtime.Config.Harvester.Search.Enabled {
		dependencies.SearchURL = runtime.Config.Harvester.Search.SearXNGURL
	}
	for _, server := range runtime.Config.MCPServers {
		if server.Enabled {
			address := "127.0.0.1:" + strconv.Itoa(runtime.Config.MCP.HTTP.Port)
			dependencies.DaemonProbe = func(context.Context) error {
				_, err := mcpserv.ProbeDaemon(address)
				return err
			}
			break
		}
	}
	return dependencies
}

// probeURL answers nil when target gives any HTTP answer within 3 s.
func probeURL(ctx context.Context, target string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return fmt.Errorf("build probe of %s: %w", target, err)
	}
	response, err := obs.WrapClient(&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("GET %s: %w", target, err)
	}
	return response.Body.Close()
}
