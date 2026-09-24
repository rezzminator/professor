package codexgen

import (
	"sync"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// codexFleetPrompt composes the Codex fleet prompt once per process — the
// same bytes the installer stages as harness-prompts/codex.md and writes into
// each Codex home's developer_instructions.
var codexFleetPrompt = sync.OnceValues(func() (string, error) {
	prompt, err := harnessprompts.Composed(pfmengine.MustLookup(pfmengine.Codex).LongName)
	if err != nil {
		return "", err
	}
	// The shared parts stay engine-neutral ON DISK: the tail writes Claude's
	// /code-review, the spelling a Claude executor runs. Codex's mapping is
	// applied HERE, at compose time, so the staged codex.md, every account's
	// developer_instructions, doctor's comparison and every role seat carry
	// the same bytes (review.go).
	return rewriteCodeReview(string(prompt), nil), nil
})

// FleetPrompt is the composed Codex fleet prompt and the ONE door every
// consumer reads it through: the installer stages it as
// harness-prompts/codex.md and writes it into each Codex home's
// developer_instructions, doctor compares an account's config against it, and
// a Codex --agent-role seat carries it ahead of the role body (agentrole). A
// compiled role file never carries it: it holds its own body only. A consumer
// composing it from the embedded parts itself would miss the Codex mappings applied here
// and read a correctly installed host as drifted.
func FleetPrompt() (string, error) { return codexFleetPrompt() }
