package installer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/restart"
)

func (installer *engine) convergeHerdrIndicators(ctx context.Context) error {
	if !installer.apply {
		return nil
	}
	dependencies := restart.Deps{
		Procs: gather.NewProcFS(installer.options.ProcRoot), Signal: syscall.Kill,
		Runner: installer.processRunner(), Clock: installer.options.Clock,
		Self: os.Getpid(), EffectiveUID: uint32(os.Geteuid()),
		Executable: filepath.Join(installer.options.Home, ".local", "bin", "pfm"),
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
