package workbench

import (
	"context"
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/codexgen"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/opencodegen"
)

// EnsureMirror builds the enabled engine's mirror before a workbench launch.
func EnsureMirror(bench Bench, engine pfmengine.ID, home string) error {
	var problems []string
	var err error
	switch engine {
	case pfmengine.Codex:
		var result codexgen.Result
		result, err = codexgen.Build(codexgen.Options{Root: bench.Dir, Home: home})
		if !result.OK {
			problems = result.Problems
		}
	case pfmengine.OpenCode:
		var result opencodegen.Result
		result, err = opencodegen.Compile(opencodegen.Options{Root: bench.Dir, Home: home, Mode: opencodegen.ModeBuild})
		if !result.OK {
			problems = result.Problems
		}
	default:
		return nil
	}
	if err == nil && len(problems) != 0 {
		err = fmt.Errorf("%s", strings.Join(problems, "; "))
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

// CheckMirror separates stale generated artifacts from a failed compiler.
func CheckMirror(bench Bench, engine pfmengine.ID, home string) (stale string, err error) {
	var problems []string
	switch engine {
	case pfmengine.Codex:
		var result codexgen.Result
		result, err = codexgen.Check(codexgen.Options{Root: bench.Dir, Home: home})
		if !result.OK {
			problems = result.Problems
		}
	case pfmengine.OpenCode:
		var result opencodegen.Result
		result, err = opencodegen.Compile(opencodegen.Options{Root: bench.Dir, Home: home, Mode: opencodegen.ModeCheck})
		if !result.OK {
			problems = result.Problems
		}
	default:
		return "", nil
	}
	if err != nil {
		obs.Logger(context.Background()).
			Error("workbench mirror check", "path", bench.Dir, obs.FieldEngine, string(engine), obs.FieldErr, err)
		return "", err
	}
	if len(problems) != 0 {
		return problems[0], nil
	}
	return "", nil
}
