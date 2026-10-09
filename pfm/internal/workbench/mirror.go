package workbench

import (
	"context"
	"fmt"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/enginesync"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// mirrorEngines are the engines with a compiled mirror, in engine-sync's
// spelling; every other engine reads its Claude sources directly.
var mirrorEngines = map[pfmengine.ID]string{
	pfmengine.Codex:    enginesync.EngineCodex,
	pfmengine.OpenCode: enginesync.EngineOpenCode,
}

// EnsureMirror builds the enabled engine's mirror before a workbench launch
// through `engine-sync {engine} build {bench} --home {home}`.
func EnsureMirror(bench Bench, engine pfmengine.ID, home string) error {
	name, mirrored := mirrorEngines[engine]
	if !mirrored {
		return nil
	}
	result, err := enginesync.Runner{Home: home}.Mirror(context.Background(), name, enginesync.ActionBuild, bench.Dir)
	if err == nil && !result.OK {
		err = fmt.Errorf("%s", strings.Join(result.Problems, "; "))
	}
	if err != nil {
		wrapped := fmt.Errorf(
			"build the %s mirror of workbench %s: %w",
			pfmengine.MustLookup(engine).LongName,
			bench.Dir,
			err,
		)
		obs.Logger(context.Background()).
			Error("workbench mirror build", "path", bench.Dir, obs.FieldEngine, string(engine), obs.FieldErr, wrapped)
		return wrapped
	}
	return nil
}

// MirrorCheck separates problems a build clears from failures it cannot repair.
type MirrorCheck struct {
	Rebuildable, Failing []string
}

// CheckMirror returns every mirror problem, split by whether a build clears
// it, from `engine-sync {engine} check {bench} --home {home}`.
func CheckMirror(bench Bench, engine pfmengine.ID, home string) (check MirrorCheck, err error) {
	name, mirrored := mirrorEngines[engine]
	if !mirrored {
		return MirrorCheck{}, nil
	}
	result, err := enginesync.Runner{Home: home}.Mirror(context.Background(), name, enginesync.ActionCheck, bench.Dir)
	problems := result.Problems
	if len(result.Rebuildable) != 0 {
		check.Rebuildable = result.Rebuildable
	}
	if err != nil {
		obs.Logger(context.Background()).
			Error("workbench mirror check", "path", bench.Dir, obs.FieldEngine, string(engine), obs.FieldErr, err)
		return MirrorCheck{}, err
	}
	rebuildable := make(map[string]int, len(check.Rebuildable))
	for _, problem := range check.Rebuildable {
		rebuildable[problem]++
	}
	for _, problem := range problems {
		if rebuildable[problem] > 0 {
			rebuildable[problem]--
		} else {
			check.Failing = append(check.Failing, problem)
		}
	}
	return check, nil
}
