package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/restart"
)

// convergeMCPDaemon releases the previous binary's listeners before a plugin starts.
func (installer *engine) convergeMCPDaemon(ctx context.Context) error {
	installer.mcpDaemonRefreshAttempted = true
	if !installer.apply {
		return installer.change("converge pfm MCP daemon before plugin setup", nil)
	}
	if installer.options.Runner == nil {
		return installer.fail(errors.New("pfm MCP daemon handoff: no service runner configured"))
	}
	if schedulerIsLaunchd {
		return installer.convergeMCPLaunchd(ctx)
	}
	if err := userSystemctl(ctx, installer.options.Runner, "show-environment"); err != nil {
		clk := installer.options.Clock
		if clk == nil {
			clk = clock.Real
		}
		dependencies := restart.Deps{
			Runner: installer.processRunner(), Procs: gather.NewProcFS(installer.options.ProcRoot),
			Signal: syscall.Kill, Clock: clk, Self: os.Getpid(), EffectiveUID: uint32(os.Geteuid()),
			Executable: installer.mcpChatCommand(), ConfigPath: installer.options.MCPConfigPath,
			LogDir: filepath.Join(installer.options.Home, ".local", "state", "pfm", "restart"), Env: os.Environ(),
		}
		dependencies.DaemonProbe = func(context.Context) error {
			_, err := mcpserv.ProbeDaemon("127.0.0.1:" + strconv.Itoa(installer.options.MCPPort))
			return err
		}
		detail, handoffErr := restart.ConvergeMCPDaemon(ctx, dependencies, installer.options.MCPEnabled[chatName])
		if handoffErr != nil {
			return installer.fail(
				fmt.Errorf(
					"pfm MCP daemon handoff: user manager unavailable: %w; unmanaged handoff: %w",
					err,
					handoffErr,
				),
			)
		}
		installer.say("pfm MCP daemon handoff: user manager unavailable (%v); %s", err, detail)
		return nil
	}
	if installer.options.MCPEnabled[chatName] {
		return installer.restartMCPUnit(ctx)
	}
	stopErr := userSystemctl(ctx, installer.options.Runner, "stop", mcpUnitName)
	if stopErr != nil && deps.ExitCode(stopErr) != 5 {
		return installer.fail(fmt.Errorf("systemctl --user stop %s: %w", mcpUnitName, stopErr))
	}
	state, err := fleetUnitState(ctx, installer.options.Runner, mcpUnitName)
	if err != nil {
		return installer.fail(fmt.Errorf("verify %s stopped: %w", mcpUnitName, errors.Join(stopErr, err)))
	}
	if running, known := unitStateRunning(state); running || !known {
		return installer.fail(
			fmt.Errorf(
				"pfm MCP daemon handoff: %s state after stop is %q; old process exit is not established",
				mcpUnitName,
				state,
			),
		)
	}
	installer.ok("pfm MCP daemon stopped: " + state)
	return nil
}

func (installer *engine) convergeMCPLaunchd(ctx context.Context) error {
	if installer.options.MCPEnabled[chatName] {
		return installer.reloadLaunchAgentWithLabel(ctx, installer.mcpLaunchAgentPath(), mcpLaunchdLabel, true)
	}
	target := "gui/" + strconv.Itoa(os.Getuid()) + "/" + mcpLaunchdLabel
	if err := installer.options.Runner.Run(ctx, "launchctl", "bootout", target); err != nil {
		installer.say("launchctl bootout %s: %v; verifying job absent", target, err)
	}
	reader, ok := installer.options.Runner.(OutputRunner)
	if !ok {
		return installer.fail(fmt.Errorf("verify launchctl %s stopped: runner cannot read output", target))
	}
	for attempt := 0; attempt < launchdBootstrapAttempts; attempt++ {
		if attempt != 0 {
			installer.pause(launchdBootstrapRetryInterval)
		}
		_, err := reader.Output(ctx, "launchctl", "print", target)
		if err != nil {
			if deps.ExitCode(err) == 113 {
				installer.ok("pfm MCP launch agent stopped: " + target)
				return nil
			}
			return installer.fail(fmt.Errorf("verify launchctl %s stopped: %w", target, err))
		}
	}
	return installer.fail(fmt.Errorf("pfm MCP daemon handoff: launchctl job %s remains loaded after bootout", target))
}
