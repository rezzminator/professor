package installer

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	// The fence is the same shape the MCP block uses (mcp.go:24-25): pfm owns
	// what lies between the two lines and nothing else in the file.
	codexInstructionsFenceBegin = "# BEGIN pfm developer_instructions — installer-owned"
	codexInstructionsFenceEnd   = "# END pfm developer_instructions — installer-owned"
	codexInstructionsKey        = "developer_instructions"
)

// mergeCodexDeveloperInstructions replaces pfm's own fenced
// developer_instructions block with the composed Codex fleet prompt. Codex
// reads this key from config.toml into the FIRST developer item of every
// session and rebuilds it verbatim after compaction, which the retired
// SessionStart hook could not do.
//
// The block leads the document: developer_instructions is a bare key, and a
// bare key appended after a table would be parsed as a member of that table.
//
// A developer_instructions the installer did not write is never destroyed —
// the config is returned unchanged and the reason is returned with it, for
// the caller to say out loud. It is a reason, not an error: nothing failed,
// pfm simply declines to overwrite an operator's own value.
func mergeCodexDeveloperInstructions(raw, prompt string) (string, string, error) {
	kept := stripCodexInstructionsFence(raw)
	var declared struct {
		Instructions *string `toml:"developer_instructions"`
	}
	if _, err := toml.Decode(kept, &declared); err != nil {
		return "", "", fmt.Errorf("parse unmanaged Codex config: %w", err)
	}
	if declared.Instructions != nil {
		return raw, fmt.Sprintf(
			"preserve hand-written %s (%d bytes); the fleet prompt is NOT installed here — "+
				"move it inside %s to let pfm own it",
			codexInstructionsKey,
			len(*declared.Instructions),
			codexInstructionsFenceBegin,
		), nil
	}
	value, err := codexTOMLString(prompt)
	if err != nil {
		return "", "", err
	}
	block := codexInstructionsFenceBegin + "\n" +
		codexInstructionsKey + " = " + value + "\n" +
		codexInstructionsFenceEnd + "\n"
	updated := block + kept
	var checked map[string]any
	if _, err := toml.Decode(updated, &checked); err != nil {
		return "", "", fmt.Errorf("the Codex config with the fleet prompt is not valid TOML: %w", err)
	}
	if instructions, _ := checked[codexInstructionsKey].(string); instructions != prompt {
		return "", "", fmt.Errorf(
			"encoded %s reads back as %d bytes, want %d",
			codexInstructionsKey,
			len(instructions),
			len(prompt),
		)
	}
	return updated, "", nil
}

// stripCodexInstructionsFence removes every previously written block, so a
// re-install replaces it instead of stacking a second declaration (which TOML
// would reject as a duplicate key). Only whole, paired fences are removed, and
// EVERY one of them: leaving a stranded earlier fence behind would hand the
// caller a config with a duplicate key it could only report, never repair.
func stripCodexInstructionsFence(raw string) string {
	if raw == "" {
		return raw
	}
	kept := []string{}
	inside := false
	for _, line := range strings.Split(raw, "\n") {
		switch {
		case line == codexInstructionsFenceBegin:
			inside = true
		case line == codexInstructionsFenceEnd && inside:
			inside = false
		case !inside:
			kept = append(kept, line)
		}
	}
	if inside {
		// An unterminated fence is not a block this function wrote; leaving
		// the file alone keeps the damage visible instead of eating the rest.
		return raw
	}
	return strings.Join(kept, "\n")
}

// codexTOMLString renders one string as a TOML value that reads back byte for
// byte. A multi-line literal string keeps the prompt readable in the config
// and processes no escapes, so backslashes and quotes survive as written; a
// prompt that a literal string cannot hold falls back to the encoder's basic
// string rather than being truncated or mangled.
func codexTOMLString(value string) (string, error) {
	if canCodexLiteral(value) {
		return "'''\n" + value + "'''", nil
	}
	encoded, err := encodeCodexValues(map[string]any{codexInstructionsKey: value})
	if err != nil {
		return "", err
	}
	_, inline, found := strings.Cut(strings.TrimSuffix(encoded, "\n"), " = ")
	if !found {
		return "", fmt.Errorf("encode %s: unexpected TOML layout %q", codexInstructionsKey, encoded)
	}
	return inline, nil
}

// canCodexLiteral reports whether a multi-line literal string holds this
// text exactly: TOML forbids the closing delimiter inside it, a closing delimiter cannot be preceded by a quote,
// and a literal string carries no escapes, so any control character but tab
// and newline would be unrepresentable. The text must end in a newline, so
// the closing delimiter starts its own line and adds no byte to the value.
func canCodexLiteral(value string) bool {
	if !strings.HasSuffix(value, "\n") || strings.Contains(value, "'''") {
		return false
	}
	for _, r := range value {
		if r == '\t' || r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
