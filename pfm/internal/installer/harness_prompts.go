package installer

import "github.com/rezzminator/professor/pfm/internal/codexgen"

// codexHarnessPrompt is the mapped Codex prompt also stored in composed/codex.md.
func codexHarnessPrompt() ([]byte, error) {
	prompt, err := codexgen.FleetPrompt()
	if err != nil {
		return nil, err
	}
	return []byte(prompt), nil
}
