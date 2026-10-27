package restart

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// The MCP daemon's supervised names: the launch agent label and the user unit.
const (
	mcpLaunchdLabel = "com.professor.pfm.mcp"
	mcpSystemdUnit  = "pfm-mcp.service"
)

// Fleet is the chat roster the restart ends: every live chat, the sockets the
// scan could not read (with why), and the end of one chat, confirmed.
type Fleet interface {
	Live(ctx context.Context) (chats []headless.Chat, unreadable map[string]string, err error)
	End(ctx context.Context, chat headless.Chat) error
}

// Deps is every host door discovery and the steps it plans go through; a test
// fills each with a fake.
type Deps struct {
	Runner   deps.Runner
	Procs    gather.ProcFS
	Signal   Signaler
	Clock    clock.Clock
	Fleet    Fleet
	Services ServiceManager
	// DaemonProbe answers nil when the loopback MCP daemon serves its status
	// document; nil when no MCP server is enabled in config.
	DaemonProbe func(ctx context.Context) error
	// Self is this process's pid; Spare are pids never stopped (the front
	// that launched this worker, which ends with its own chat).
	Self  int
	Spare []int
	// EffectiveUID is this process's user: a process table row of another
	// user that cannot be read is none of the restart's.
	EffectiveUID uint32
	// Executable and ConfigPath start pfm again (the unsupervised MCP daemon).
	Executable  string
	ConfigPath  string
	HerdrBinary string
	// LaunchedFrom is the address of the chat that typed the restart; it ends
	// last of the chats.
	LaunchedFrom chatAddress
	// DryRun marks a plan only printed: no worker runs it.
	DryRun bool
	// LogDir holds the logs of the processes the restart starts detached.
	LogDir string
	// Env is the environment of every process the restart starts.
	Env []string
}

// discoverer builds one plan.
type discoverer struct {
	deps    Deps
	plan    Plan
	claimed map[int]bool
}

func (finder *discoverer) found(family, detail string) {
	finder.plan.Findings = append(finder.plan.Findings, Finding{Family: family, Detail: detail})
}

func (finder *discoverer) failed(family string, err error) {
	finder.plan.Findings = append(finder.plan.Findings, Finding{Family: family, Err: err})
}

// BuildPlan finds everything the restart stops and starts, in stop order, each
// unit's state read once, before anything stops. It only reads: the fleet
// scan, the process table, the service managers' state, `herdr status`,
// `docker ps` and `docker inspect`.
func BuildPlan(ctx context.Context, dependencies Deps) Plan {
	finder := &discoverer{deps: dependencies, claimed: map[int]bool{}}
	finder.findChats(ctx)
	table, tableErr := finder.readTable()
	if tableErr != nil {
		finder.failed(FamilyHelpers, tableErr)
	} else {
		finder.findHelpers(table)
	}
	finder.findHerdr(ctx, table, tableErr)
	supervised, servicesErr := finder.findServices(ctx)
	if tableErr != nil {
		finder.failed(FamilyMCPDaemon, tableErr)
	} else {
		finder.findMCPDaemon(table, supervised, servicesErr)
	}
	if tableErr != nil {
		finder.failed(FamilyPFM, tableErr)
	} else {
		finder.findPFM(table)
	}
	finder.plan.Last = fmt.Sprintf(
		"this restart worker, pid %d, detached from every chat — exits after the start phase, the last pfm process",
		dependencies.Self)
	if dependencies.DryRun {
		finder.plan.Last = "the restart worker a plain pfm restart detaches (pfm internal " + WorkerCommand +
			") — exits after the start phase, the last pfm process"
	}
	return finder.plan
}

// findChats is the chats family: every live Claude, Codex and OpenCode chat
// the fleet scan sees, the launching chat last. A socket the scan could not
// read is a failed find, named.
func (finder *discoverer) findChats(ctx context.Context) {
	if finder.deps.Fleet == nil {
		finder.failed(FamilyChats, fmt.Errorf("no fleet to scan"))
		return
	}
	chats, unreadable, err := finder.deps.Fleet.Live(ctx)
	if err != nil {
		finder.failed(FamilyChats, fmt.Errorf("fleet scan: %w", err))
		return
	}
	sort.SliceStable(chats, func(left, right int) bool {
		return !finder.deps.LaunchedFrom.holds(chats[left]) && finder.deps.LaunchedFrom.holds(chats[right])
	})
	finder.found(
		FamilyChats,
		fmt.Sprintf("%d live chat(s) — fleet scan of every tmux socket and Herdr seat", len(chats)),
	)
	sockets := make([]string, 0, len(unreadable))
	for socket := range unreadable {
		sockets = append(sockets, socket)
	}
	sort.Strings(sockets)
	for _, socket := range sockets {
		finder.failed(FamilyChats, fmt.Errorf("tmux socket %s could not be read, its chats are unknown: %s",
			socket, unreadable[socket]))
	}
	for index := range chats {
		finder.plan.Units = append(finder.plan.Units, finder.chatUnit(chats[index]))
	}
}

func (finder *discoverer) chatUnit(chat headless.Chat) Unit {
	engine := string(chat.Engine)
	if descriptor, err := pfmengine.Lookup(chat.Engine); err == nil {
		engine = descriptor.LongName
	}
	name := chat.Name
	if name == "" {
		name = "(unnamed)"
	}
	found := "fleet scan: tmux socket " + chat.Socket + " pane " + chat.Pane
	stop := "tmux kill-server on " + chat.Socket + "; verified: the socket no longer answers"
	if chat.Backend == spawn.BackendHerdr {
		found = "fleet scan: Herdr seat " + chat.SeatID
		stop = "Herdr pane.close of the proved seat; verified: the seat is retired"
	}
	if finder.deps.LaunchedFrom.holds(chat) {
		found += " (the chat that typed pfm restart)"
	}
	fleet := finder.deps.Fleet
	return Unit{
		Family:  FamilyChats,
		Name:    fmt.Sprintf("%s %s session %s", engine, name, chat.ID),
		FoundBy: found,
		StopHow: stop + "; not brought back",
		Stop: func(ctx context.Context) (string, error) {
			if err := fleet.End(ctx, chat); err != nil {
				return "", err
			}
			return "ended", nil
		},
		Chat: &EndedChat{Engine: engine, Name: name, SessionID: chat.ID},
	}
}

// findServices is the services family: every launch agent or user unit pfm
// installs. It reports whether the MCP daemon is among them and was up: a
// daemon unit that stays stopped supervises nothing, so a daemon run by hand
// beside it is the mcp-daemon family's. A failed find returns its error: who
// supervises the daemon is then unknown, never "nobody".
func (finder *discoverer) findServices(ctx context.Context) (bool, error) {
	if finder.deps.Services == nil {
		finder.found(FamilyServices, "no service manager on this platform — no supervised services")
		return false, nil
	}
	units, detail, err := finder.deps.Services.Units(ctx, FamilyServices, func(name string) bool {
		return strings.HasPrefix(name, pfmLaunchdPrefix) || strings.HasPrefix(name, pfmSystemdPrefix)
	}, func(name string) func(context.Context) error {
		if name == mcpLaunchdLabel || name == mcpSystemdUnit {
			return finder.deps.DaemonProbe
		}
		return nil
	})
	if err != nil {
		finder.failed(FamilyServices, err)
		return false, err
	}
	finder.found(FamilyServices, detail)
	finder.plan.Units = append(finder.plan.Units, units...)
	for index := range units {
		unit := &units[index]
		if !unit.StaysStopped && (unit.Name == "launchd "+mcpLaunchdLabel || unit.Name == "systemd "+mcpSystemdUnit) {
			return true, nil
		}
	}
	return false, nil
}

// findMCPDaemon is the loopback MCP daemon run by hand (a container or an
// unmanaged host). A supervised daemon belongs to its service unit: its pids
// are claimed so nothing stops them twice. When the services find failed, a
// daemon may be supervised: it is claimed and left running, a failed find.
func (finder *discoverer) findMCPDaemon(table processTable, supervised bool, servicesErr error) {
	defer finder.failedOn(FamilyMCPDaemon, table.unreadableErr())
	var daemons []process
	for _, proc := range table.Processes {
		if finder.mcpDaemon(proc.Argv) && !finder.claimed[proc.PID] {
			finder.claimed[proc.PID] = true
			daemons = append(daemons, proc)
		}
	}
	if servicesErr != nil {
		finder.failed(FamilyMCPDaemon, fmt.Errorf(
			"the services find failed, so whether a service unit supervises the daemon is unknown — "+
				"%d pfm mcp serve process(es) left running: %w", len(daemons), servicesErr))
		return
	}
	if supervised {
		finder.found(FamilyMCPDaemon, fmt.Sprintf(
			"supervised by its service unit (%d daemon process(es)) — it stops and starts with that unit",
			len(daemons),
		))
		return
	}
	finder.found(FamilyMCPDaemon, fmt.Sprintf(
		"%d unsupervised pfm mcp serve process(es) of %d listed — process table", len(daemons), table.Listed))
	for index, proc := range daemons {
		unit := finder.processUnit(FamilyMCPDaemon, "pfm mcp serve", proc)
		if index == 0 {
			unit.StartHow = finder.daemonArgv() + ", detached; verified: the loopback /status probe answers"
			unit.Start = finder.startDaemon
		}
		finder.plan.Units = append(finder.plan.Units, unit)
	}
}

func (finder *discoverer) daemonCommand() []string {
	argv := []string{finder.deps.Executable}
	if finder.deps.ConfigPath != "" {
		argv = append(argv, "--config", finder.deps.ConfigPath)
	}
	return append(argv, "mcp", "serve")
}

func (finder *discoverer) daemonArgv() string { return strings.Join(finder.daemonCommand(), " ") }

// startDaemon starts the loopback MCP daemon detached and waits for its
// status document.
func (finder *discoverer) startDaemon(ctx context.Context) (string, error) {
	if finder.deps.DaemonProbe == nil {
		return "", fmt.Errorf("no MCP server is enabled in config, so the daemon has no port to prove")
	}
	log, logPath, err := openStartLog(finder.deps.LogDir, "mcp-serve.log")
	if err != nil {
		return "", err
	}
	defer func() { _ = log.Close() }() // the detached daemon holds its own copy
	process, err := finder.deps.Runner.Start(ctx, finder.daemonCommand(), deps.StartOptions{
		Detach: true, Env: finder.deps.Env, Stdout: log, Stderr: log,
	})
	if err != nil {
		return "", fmt.Errorf("start %s: %w", finder.daemonArgv(), err)
	}
	tools := serviceTools{runner: finder.deps.Runner, clock: finder.deps.Clock}
	if err := tools.probeUp(ctx, finder.deps.DaemonProbe); err != nil {
		return "", fmt.Errorf("pid %d: /status not answering: %w (log %s)", process.Pid(), err, logPath)
	}
	return fmt.Sprintf("pid %d serving; /status answered", process.Pid()), nil
}
