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
	// developer_instructions, doctor's comparison and every compiled role
	// carry the same bytes (review.go).
	return rewriteCodeReview(string(prompt), nil), nil
})

// FleetPrompt is the composed Codex fleet prompt and the ONE door every
// consumer reads it through: the installer stages it as
// harness-prompts/codex.md and writes it into each Codex home's
// developer_instructions, doctor compares an account's config against it, and
// every compiled role prepends it to its own body. A consumer composing it
// from the embedded parts itself would miss the Codex mappings applied here
// and read a correctly installed host as drifted.
func FleetPrompt() (string, error) { return codexFleetPrompt() }

// fleetRoleInstructions is what a compiled role's developer_instructions
// holds: the fleet prompt, then the role's own body.
//
// A role file's developer_instructions REPLACES the config-level value
// instead of extending it (codex-rs/core/src/agent/role.rs:187-190), and the
// fork path then substitutes the parent's text out of the inherited history —
// so a role carrying only its own body would run with no fleet prompt at all.
// Prepending here is what keeps a role sub-agent a fleet member.
func fleetRoleInstructions(body string) (string, error) {
	prompt, err := codexFleetPrompt()
	if err != nil {
		return "", err
	}
	return prompt + "\n---\n\n" + body, nil
}
