package installer

import "github.com/rezzminator/professor/pfm/internal/codexprompt"

// codexHarnessPrompt is the mapped Codex prompt also stored in composed/codex.md.
func codexHarnessPrompt() ([]byte, error) {
	prompt, err := codexprompt.FleetPrompt()
	if err != nil {
		return nil, err
	}
	return []byte(prompt), nil
}
