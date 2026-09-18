package doctor

import (
	"context"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/harvestpy"
)

// pinnedHarvestDoctor is the production conversion health check. Its Runner
// is bound by printHarvestPythonDoctorWithRunner before the check runs.
type pinnedHarvestDoctor struct {
	runner deps.Runner
}

func (pinnedHarvestDoctor) Inspect(root string, platform harvestpy.Platform) (harvestpy.EnvironmentDigest, error) {
	return harvestpy.InspectConversionEnvironment(root, platform)
}

func (p pinnedHarvestDoctor) Check(
	ctx context.Context,
	root string,
	platform harvestpy.Platform,
) (harvestpy.CheckReport, error) {
	return harvestpy.CheckConversionEnvironmentWithRunner(ctx, root, platform, p.runner)
}

type doctorBrowserSmokeRunnerKey struct{}

// doctorBrowserSmoke runs the worker's no-launch smoke probe LIVE — the same
// verdict path a fetch would trust. Injectable so tests simulate smoke
// results without provisioning an environment.
var doctorBrowserSmoke = func(ctx context.Context, interpreter, script string) (map[string]any, error) {
	runner, _ := ctx.Value(doctorBrowserSmokeRunnerKey{}).(deps.Runner)
	if runner == nil {
		runner = deps.RealRunner{}
	}
	worker := harvestpy.NewBrowserWorker(harvestpy.Runtime{Python: interpreter, Script: script, Runner: runner})
	return worker.Smoke(ctx)
}

func runDoctorBrowserSmokeWithRunner(
	ctx context.Context,
	interpreter string,
	script string,
	runner deps.Runner,
) (map[string]any, error) {
	if runner == nil {
		runner = deps.RealRunner{}
	}
	return doctorBrowserSmoke(
		context.WithValue(ctx, doctorBrowserSmokeRunnerKey{}, runner),
		interpreter,
		script,
	)
}
