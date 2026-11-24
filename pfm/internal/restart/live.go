package restart

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

// liveFleet is the real chat roster: the fleet scan, chat.EndForRestart, and
// the chat doors that judge and verify a Herdr chat's restore.
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
		// The shared addressable set, minus an agent row: restart ends chats, never an agent it did not start.
		if row.Kind.IsAddressable() && row.Kind != compose.Agent {
			chats = append(chats, pfmchat.FromRow(row))
		}
	}
	return chats, scan.Unreadable, nil
}

func (fleet liveFleet) End(ctx context.Context, chat headless.Chat) error {
	return pfmchat.EndForRestart(ctx, chat, fleet.runtime.Paths)
}

func (fleet liveFleet) Restorable(ctx context.Context, chat headless.Chat) (string, pfmchat.RestartSeatHost, error) {
	return pfmchat.RestartRestorable(ctx, chat, fleet.runtime.Paths)
}

func (fleet liveFleet) Back(
	ctx context.Context,
	chat headless.Chat,
	socket string,
	before pfmchat.RestartSeatHost,
) (bool, string, error) {
	return pfmchat.RestartBack(ctx, chat, socket, before, fleet.runtime.Paths, clock.Real)
}

// LiveDeps is the host's real doors for one restart.
func LiveDeps(runtime config.Runtime, warn io.Writer, spare []int, launchedFrom chatAddress) Deps {
	runner := obs.Runner(deps.RealRunner{})
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(warn, "pfm restart: resolve this binary (pfm processes are matched by name only): %v\n", err)
	}
	services := nativeServiceManager(
		servicemanager.Native(),
		runner,
		clock.Real,
		runtime.Paths.Home,
		servicemanager.UID(),
	)
	dependencies := Deps{
		Runner:       runner,
		Procs:        gather.NewProcFS(runtime.Paths.ProcRoot),
		Kernel:       gather.NativeKernel(),
		Probe:        gather.ProbeProcess,
		Install:      gather.InstallAt(runtime.Paths.Home),
		Clock:        clock.Real,
		Fleet:        liveFleet{runtime: runtime, warn: warn},
		Services:     services,
		Self:         os.Getpid(),
		EffectiveUID: uint32(os.Geteuid()),
		Spare:        spare,
		Executable:   executable,
		ConfigPath:   runtime.Config.Path,
		HerdrBinary:  (paths.OSEnv{}).Get(herdr.EnvBin),
		LaunchedFrom: launchedFrom,
		LogDir:       restartLogDir(runtime.Paths),
		Env:          restartWorkerEnv(os.Environ()),
		ResumeOnRestore: func() (bool, error) {
			return herdr.ResumeOnRestore(paths.OSEnv{})
		},
	}
	if config.MCPServerEnabled(runtime.Config, config.MCPServerChat) {
		address := "127.0.0.1:" + strconv.Itoa(runtime.Config.MCP.HTTP.Port)
		dependencies.DaemonProbe = func(context.Context) error {
			_, err := mcpserv.ProbeDaemon(address)
			return err
		}
	}
	return dependencies
}
