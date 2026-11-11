package reload

import (
	"github.com/rezzminator/professor/pfm/internal/action"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// applyReloadWorkbench reads the persona through the launch doors' effort
// validation. Unlike a new launch it refuses a persona it cannot apply: the
// refusal comes before the pane is touched, so the live chat stays as it is.
func applyReloadWorkbench(request Request) (Request, error) {
	persona, err := action.WorkbenchPersona(request.CWD, request.Engine, workbench.Resume)
	if err != nil {
		return request, err
	}
	request.Effort = persona.EffortOr(request.Effort)
	request.Model = persona.ModelOr(request.Model)
	if request.Engine == pfmengine.Codex && persona.Applies() {
		if err := workbench.EnsureMirror(persona.Bench, pfmengine.Codex, request.Home); err != nil {
			return request, err
		}
	}
	return request, nil
}
