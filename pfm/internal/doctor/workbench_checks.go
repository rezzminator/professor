package doctor

import (
	"context"
	"fmt"
	"io"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/professor"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

func printWorkbenchDoctor(stdout io.Writer, start, home string) (warnings, failures int) {
	root, found, err := professor.ResolveProjectRoot(start)
	if err != nil {
		obs.Logger(context.Background()).Error("workbench doctor root", "path", start, obs.FieldErr, err)
		fmt.Fprintf(stdout, "doctor: workbench UNREADABLE %v\n", err)
		return 0, 1
	}
	if !found {
		fmt.Fprintln(stdout, "doctor: workbench none — not inside a Professor project")
		return 0, 0
	}
	benches, walkErrors := workbench.Discover([]string{root})
	if len(benches) == 0 && len(walkErrors) == 0 {
		fmt.Fprintf(stdout, "doctor: workbench none under %s\n", root)
	}
	for i := range benches {
		bench := &benches[i]
		if bench.Err != nil {
			fmt.Fprintf(stdout, "doctor: workbench %s FAILED: %v\n", bench.Dir, bench.Err)
			failures++
			continue
		}
		engines := make([]string, 0, len(bench.Engines))
		for _, engine := range bench.Engines {
			engines = append(engines, pfmengine.MustLookup(engine).LongName)
		}
		beforeFailures := failures
		for _, engine := range bench.Engines {
			if engine != pfmengine.Codex && engine != pfmengine.OpenCode {
				continue
			}
			check, err := workbench.CheckMirror(*bench, engine, home)
			if err != nil {
				fmt.Fprintf(
					stdout,
					"doctor: workbench %s %s mirror BROKEN: %v\n",
					bench.Dir,
					pfmengine.MustLookup(engine).LongName,
					err,
				)
				failures++
				continue
			}
			name := pfmengine.MustLookup(engine).LongName
			for _, problem := range check.Failing {
				fmt.Fprintf(
					stdout,
					"doctor: workbench %s %s mirror FAILED: %s — the next launch there fails; fix it, then run pfm %s build %s\n",
					bench.Dir,
					name,
					problem,
					name,
					bench.Dir,
				)
				failures++
			}
			if len(check.Rebuildable) != 0 {
				repair := "the next launch there rebuilds it"
				if len(check.Failing) != 0 {
					repair = fmt.Sprintf("rebuilt by pfm %s build %s once the failures are fixed", name, bench.Dir)
				}
				fmt.Fprintf(stdout, "doctor: workbench %s %s mirror STALE: %s — %s\n",
					bench.Dir, name, strings.Join(check.Rebuildable, "; "), repair)
				warnings++
			}
		}
		if failures == beforeFailures {
			fmt.Fprintf(
				stdout,
				"doctor: workbench %s ok · engines %s · prompt %s\n",
				bench.Dir,
				strings.Join(engines, ","),
				bench.Prompt,
			)
		}
	}
	for _, walkError := range walkErrors {
		fmt.Fprintf(
			stdout,
			"doctor: workbench discovery FAILED: %s — whether more workbenches exist there is UNKNOWN\n",
			walkError.Error(),
		)
		failures++
	}
	return warnings, failures
}
