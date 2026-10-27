package opencode

import (
	"github.com/rezzminator/professor/pfm/internal/action"
)

type HeadlessPlanner struct{}

func (HeadlessPlanner) Plan(request action.HeadlessRequest) (action.HeadlessPlan, error) {
	return action.PlanOpenCode(request)
}
