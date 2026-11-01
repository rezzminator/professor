package installer

import (
	"context"
	"fmt"
	"os"

	"github.com/rezzminator/professor/pfm/internal/agentlauncher"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/restart"
)

func (installer *engine) convergeHerdrIndicators(ctx context.Context) error {
	if !installer.apply {
		return nil
	}
	dependencies := restart.Deps{
		Procs: gather.NewProcFS(installer.options.ProcRoot), Kernel: gather.NativeKernel(),
		Probe: gather.ProbeProcess, Install: gather.InstallAt(installer.options.Home),
		Runner: installer.processRunner(), Clock: installer.options.Clock,
		Self: os.Getpid(), EffectiveUID: uint32(os.Geteuid()),
		Executable: agentlauncher.ExecTarget(installer.options.Home),
	}
	if installer.options.herdrIndicatorDeps != nil {
		dependencies = *installer.options.herdrIndicatorDeps
	}
	detail, replaced, err := restart.ConvergeHerdrIndicators(ctx, dependencies)
	if err != nil {
		return installer.fail(fmt.Errorf("refresh Herdr indicators: %w", err))
	}
	if replaced {
		return installer.change(detail, nil)
	}
	installer.ok(detail)
	return nil
}
