package action

import (
	"fmt"
	"sort"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

type HeadlessPlanner interface {
	Plan(request HeadlessRequest) (HeadlessPlan, error)
}

var planners = map[pfmengine.ID]HeadlessPlanner{}

func RegisterPlanner(id pfmengine.ID, planner HeadlessPlanner) {
	if _, duplicate := planners[id]; duplicate {
		panic(fmt.Sprintf("action: headless planner for engine %q registered twice", id))
	}
	planners[id] = planner
}

func PlannerFor(id pfmengine.ID) (HeadlessPlanner, error) {
	planner, ok := planners[id]
	if !ok {
		if descriptor, err := pfmengine.Lookup(id); err == nil {
			return nil, fmt.Errorf("%s does not support headless chat", descriptor.Short)
		}
		return nil, fmt.Errorf("engine %s: no headless planner registered", id)
	}
	return planner, nil
}

func RegisteredPlanners() []pfmengine.ID {
	ids := make([]pfmengine.ID, 0, len(planners))
	for id := range planners {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	return ids
}
