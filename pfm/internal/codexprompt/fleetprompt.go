// Package codexprompt composes the Codex fleet prompt, the one door every
// pfm consumer reads it through: the installer's generated harness-prompts/generated/codex.md
// and each Codex home's developer_instructions, doctor's comparison, and a Codex
// --agent-role seat. Compiling Claude sources for Codex is engine-sync's; the
// fleet prompt stays pfm's.
package codexprompt

import (
	"sync"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// codexFleetPrompt composes the Codex fleet prompt once per process — the
// same bytes the installer generates as harness-prompts/generated/codex.md and writes into
// each Codex home's developer_instructions.
var codexFleetPrompt = sync.OnceValues(func() (string, error) {
	prompt, err := harnessprompts.Composed(pfmengine.MustLookup(pfmengine.Codex).LongName)
	if err != nil {
		return "", err
	}
	// The shared parts stay engine-neutral ON DISK, so a part that spells
	// Claude's /code-review (none does today) keeps the spelling a Claude
	// executor runs. Codex's mapping is applied HERE, at compose time, so the
	// generated codex.md, every account's developer_instructions and doctor's
	// comparison carry the same bytes; a role body, which is Claude-authored
	// text of its own, takes the same mapping in agentrole (review.go).
	return RewriteCodeReview(string(prompt)), nil
})

// FleetPrompt is the composed Codex fleet prompt and the ONE door every
// consumer reads it through: the installer generates it as
// harness-prompts/generated/codex.md and writes it into each Codex home's
// developer_instructions, doctor compares an account's config against it, and
// a Codex --agent-role seat carries it ahead of the role body (agentrole). A
// compiled role file never carries it: it holds its own body only. A consumer
// composing it from the embedded parts itself would miss the Codex mappings applied here
// and read a correctly installed host as drifted.
func FleetPrompt() (string, error) { return codexFleetPrompt() }
