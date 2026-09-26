package ask

import (
	"fmt"
	"sort"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

type Runner interface {
	Resolve(machine pfmconfig.Config) (Engine, error)
}

var runners = map[pfmengine.ID]Runner{}

func RegisterRunner(id pfmengine.ID, runner Runner) {
	if _, duplicate := runners[id]; duplicate {
		panic(fmt.Sprintf("ask: runner for engine %q registered twice", id))
	}
	runners[id] = runner
}

func RunnerFor(id pfmengine.ID) (Runner, error) {
	runner, ok := runners[id]
	if !ok {
		if descriptor, err := pfmengine.Lookup(id); err == nil {
			return nil, fmt.Errorf("%s does not support ask", descriptor.Short)
		}
		return nil, fmt.Errorf("engine %s: no ask runner registered", id)
	}
	return runner, nil
}

func RegisteredRunners() []pfmengine.ID {
	ids := make([]pfmengine.ID, 0, len(runners))
	for id := range runners {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	return ids
}
