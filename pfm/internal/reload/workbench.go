package reload

import (
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

func applyReloadWorkbench(request Request) (Request, error) {
	persona, err := workbench.ForLaunch(request.CWD, request.Engine, workbench.Resume)
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
