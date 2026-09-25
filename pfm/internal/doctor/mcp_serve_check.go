package doctor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/stale"
)

// printMCPServeProcessesDoctor reports chat stdio servers still executing a
// pfm binary that an install has replaced. It only observes processes; the
// explicit `pfm internal stale --sweep` command remains the remedy.
func printMCPServeProcessesDoctor(stdout io.Writer, runtime config.Runtime, table gather.ProcFS) (warnings int) {
	return printMCPServeProcessesDoctorWithSignaler(stdout, runtime, table, syscall.Kill)
}

func printMCPServeProcessesDoctorWithSignaler(
	stdout io.Writer,
	runtime config.Runtime,
	table gather.ProcFS,
	signal stale.Signaler,
) (warnings int) {
	return printMCPServeProcessesDoctorWithSignalerAndUID(
		stdout,
		runtime,
		table,
		signal,
		uint32(os.Geteuid()),
	)
}

func printMCPServeProcessesDoctorWithSignalerAndUID(
	stdout io.Writer,
	runtime config.Runtime,
	table gather.ProcFS,
	signal stale.Signaler,
	effectiveUID uint32,
) (warnings int) {
	binary := filepath.Join(runtime.Paths.Home, ".local", "bin", "pfm")
	snapshot, candidates, probeFailures, err := snapshotMCPServeProcesses(table, effectiveUID)
	if err != nil {
		fmt.Fprintf(stdout, "doctor: mcp-serve UNREAD — %v\n", err)
		return 1
	}
	scan, err := stale.Find(snapshot, binary, signal)
	if err != nil {
		fmt.Fprintf(stdout, "doctor: mcp-serve UNREAD — %v\n", err)
		return 1
	}

	obsolete, compatible, classifyErr := stale.ClassifyCompatibleProxies(
		snapshot,
		runtime.Paths.ProcRoot,
		runtime.Paths.Home,
		signal,
		scan.Stale,
	)
	renderedCandidate := false
	if classifyErr != nil {
		// Classification only tells a compatible proxy from an obsolete one;
		// failing it never hides that each candidate runs a replaced image.
		fmt.Fprintf(stdout, "doctor: mcp-serve UNREAD — %v\n", classifyErr)
		warnings++
		for _, process := range scan.Stale {
			if !candidates[process.PID] {
				continue
			}
			printMCPServeProcess(stdout, table, "STALE", process)
			renderedCandidate = true
			warnings++
		}
	} else {
		for _, process := range obsolete {
			if !candidates[process.PID] {
				continue
			}
			printMCPServeProcess(stdout, table, "STALE", process)
			renderedCandidate = true
			warnings++
		}
		for _, process := range compatible {
			if !candidates[process.PID] {
				continue
			}
			printMCPServeProcess(stdout, table, "COMPATIBLE", process)
			renderedCandidate = true
		}
	}
	for _, unreadable := range scan.Unreadable {
		for pid := range candidates {
			if strings.HasPrefix(unreadable, "pid="+strconv.Itoa(pid)+"  ") {
				fmt.Fprintf(stdout, "doctor: mcp-serve UNREAD — %s\n", unreadable)
				warnings++
				break
			}
		}
	}
	for _, failure := range probeFailures {
		if printMCPServeProbeFailure(stdout, signal, failure) {
			warnings++
		}
	}
	if warnings == 0 && !renderedCandidate {
		fmt.Fprintf(stdout, "doctor: mcp-serve clean checked=%d\n", len(candidates))
	}
	return warnings
}

func printMCPServeProcess(stdout io.Writer, table gather.ProcFS, status string, process stale.Process) {
	fmt.Fprintf(
		stdout,
		"doctor: mcp-serve %s pid=%d chat=%s command=%s\n",
		status,
		process.PID,
		mcpServeChat(table, process.PID),
		process.Command,
	)
}

type mcpServeProcessSnapshot struct {
	gather.ProcFS
	pids       []int
	cmdlines   map[int][]string
	cmdlineErr map[int]error
}

type mcpServeProbeFailure struct {
	pid  int
	step string
	err  error
}

func (snapshot *mcpServeProcessSnapshot) PIDs() ([]int, error) {
	return snapshot.pids, nil
}

func (snapshot *mcpServeProcessSnapshot) Cmdline(pid int) ([]string, error) {
	return snapshot.cmdlines[pid], snapshot.cmdlineErr[pid]
}

type mcpServeImageSnapshot struct {
	*mcpServeProcessSnapshot
	images gather.ProcImage
}

func (snapshot mcpServeImageSnapshot) Image(pid int) (gather.FileID, error) {
	return snapshot.images.Image(pid)
}

// snapshotMCPServeProcesses reads each pid's command once and names the
// mcp-serve candidates. A pid whose identity cannot be read is judged by its
// command: a candidate is checked and its identity failure reported once, a
// pid whose command fails too gets one row naming both steps, and any other
// command drops it.
func snapshotMCPServeProcesses(
	table gather.ProcFS,
	effectiveUID uint32,
) (gather.ProcFS, map[int]bool, []mcpServeProbeFailure, error) {
	pids, err := table.PIDs()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list processes: %w", err)
	}
	snapshot := &mcpServeProcessSnapshot{
		ProcFS:     table,
		pids:       make([]int, 0, len(pids)),
		cmdlines:   make(map[int][]string, len(pids)),
		cmdlineErr: make(map[int]error),
	}
	candidates := make(map[int]bool)
	identities, hasIdentities := table.(gather.ProcIdentity)
	var failures []mcpServeProbeFailure
	for _, pid := range pids {
		var identityErr error
		if hasIdentities {
			identity, err := identities.ProcessIdentity(pid)
			switch {
			case err != nil:
				identityErr = err
			case identity.EffectiveUID != effectiveUID || identity.Command != "" && identity.Command != "pfm":
				continue
			case identity.Command == "":
				identityErr = errors.New("empty command name")
			}
		}
		snapshot.pids = append(snapshot.pids, pid)
		argv, commandErr := table.Cmdline(pid)
		snapshot.cmdlines[pid] = argv
		snapshot.cmdlineErr[pid] = commandErr
		switch {
		case commandErr != nil && identityErr != nil:
			failures = append(failures, mcpServeProbeFailure{
				pid: pid, step: "read process identity",
				err: fmt.Errorf("%w; read command: %w", identityErr, commandErr),
			})
		case commandErr != nil:
			failures = append(failures, mcpServeProbeFailure{pid: pid, step: "read command", err: commandErr})
		case mcpServeCandidate(argv):
			candidates[pid] = true
			if identityErr != nil {
				failures = append(failures, mcpServeProbeFailure{
					pid: pid, step: "read process identity", err: identityErr,
				})
			}
		}
	}
	if images, ok := table.(gather.ProcImage); ok {
		return mcpServeImageSnapshot{
			mcpServeProcessSnapshot: snapshot,
			images:                  images,
		}, candidates, failures, nil
	}
	return snapshot, candidates, failures, nil
}

// mcpServeCandidate reports whether argv is a pfm chat MCP stdio server:
// `mcp serve [--stdio]`, plus the stdio argv a chat launched before the
// professor server (bare `mcp`, `mcp <server> serve …`) — a binary upgrade
// leaves those running on the replaced image.
func mcpServeCandidate(argv []string) bool {
	if len(argv) == 0 || filepath.Base(argv[0]) != "pfm" {
		return false
	}
	command := argv[1:]
	switch {
	case len(command) >= 2 && command[0] == "--config":
		command = command[2:]
	case len(command) >= 1 && strings.HasPrefix(command[0], "--config="):
		command = command[1:]
	}
	return len(command) >= 1 && command[0] == "mcp" && (len(command) == 1 || command[1] == "serve" ||
		len(command) >= 3 && command[2] == "serve")
}

func printMCPServeProbeFailure(
	stdout io.Writer,
	signal stale.Signaler,
	failure mcpServeProbeFailure,
) bool {
	livenessErr := signal(failure.pid, 0)
	if errors.Is(livenessErr, syscall.ESRCH) {
		return false
	}
	fmt.Fprintf(stdout, "doctor: mcp-serve UNREAD — pid=%d  %s: %v", failure.pid, failure.step, failure.err)
	if livenessErr != nil {
		fmt.Fprintf(stdout, "; probe liveness with signal 0: %v", livenessErr)
	}
	fmt.Fprintln(stdout)
	return true
}

func mcpServeChat(table gather.ProcFS, pid int) string {
	environment, err := table.Environ(pid)
	if err != nil {
		return "UNRESOLVED"
	}
	for _, name := range []string{resolve.ClaudeSessionEnv, resolve.CodexThreadEnv} {
		if value := environment[name]; value != "" {
			return value
		}
	}
	socket, _, _ := strings.Cut(environment["TMUX"], ",")
	pane := environment["TMUX_PANE"]
	if socket != "" && pane != "" {
		return socket + ":" + pane
	}
	if pane != "" {
		return pane
	}
	if socket != "" {
		return socket
	}
	return "UNRESOLVED"
}
