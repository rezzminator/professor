package reload

import (
	"io"

	"github.com/rezzminator/professor/pfm/internal/action"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// codexHomeEnv is the CODEX_HOME assignment a Codex respawn carries ahead of
// the settings envs (codexRun), none for an empty home.
func codexHomeEnv(home string) []string {
	if home == "" {
		return nil
	}
	return []string{"CODEX_HOME=" + home}
}

// applyReloadWorkbench reads the persona through the launch doors' effort
// validation. Unlike a new launch it refuses a persona it cannot apply: the
// refusal comes before the pane is touched, so the live chat stays as it is.
// A settings file the respawn goes on without is named on stderr.
func applyReloadWorkbench(request Request, stderr io.Writer) (Request, error) {
	persona, err := action.WorkbenchPersona(request.CWD, request.Home, request.Engine, workbench.Resume)
	if err != nil {
		return request, err
	}
	if stderr != nil {
		launchnotice.Print(stderr, "pfm chat reload", persona.Warnings)
	}
	request.Effort = persona.EffortOr(request.Effort)
	request.Model = persona.ModelOr(request.Model)
	request.Envs = persona.Envs
	if request.Engine == pfmengine.Codex && persona.Applies() {
		if err := workbench.EnsureMirror(persona.Bench, pfmengine.Codex, request.Home); err != nil {
			return request, err
		}
	}
	return request, nil
}
