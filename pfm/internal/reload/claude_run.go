package reload

import (
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/action"
)

// claudeRun is the respawn line for a Claude seat. It owns nothing: the strip,
// the autonomy posture and the system prompt all come from the one spawn door,
// so a chat that reboots in place comes back with exactly what a fresh launch
// would have carried.
func claudeRun(request Request) (string, error) {
	effort, err := action.ClaudeEffort(request.Effort)
	if err != nil {
		return "", fmt.Errorf("resolve claude respawn effort: %w", err)
	}
	spawn := action.ClaudeSpawn{
		Purpose:    action.PurposeResume,
		Account:    request.Account,
		Cache1H:    &request.Cache1H,
		Home:       request.Home,
		Machine:    request.Machine,
		Model:      request.Model,
		Effort:     effort,
		PromptFile: request.PromptChannel,
		Name:       request.Name,
	}
	if request.fresh {
		spawn.Purpose, spawn.SessionID = action.PurposeInteractive, request.SessionID
	} else {
		spawn.Resume = request.SessionID
	}
	run, err := spawn.ShellCommand()
	if err != nil {
		return "", fmt.Errorf("render claude respawn command: %w", err)
	}
	return run, nil
}
