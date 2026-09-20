package installer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	pfmengine "hostops/pfm/internal/engine"
)

// openCodeInstructionsKey is the machine-scope config array OpenCode reads
// extra system-prompt files from. OpenCode has no --system-prompt-file and no
// appendix hook: `Instruction.systemPaths` resolves every entry of this array
// (absolute paths globbed, `~/` expanded) and `Instruction.system` reads each
// one into the session's system array, so naming the staged prompt here is
// the ONE door pfm has into an OpenCode system prompt.
const openCodeInstructionsKey = "instructions"

// wireOpenCodeInstructions points OpenCode's config at the staged OpenCode
// prompt, preserving every other key and every entry the operator wrote. Our
// entry leads, so the Professor arrives ahead of an operator's additions.
func (installer *engine) wireOpenCodeInstructions() error {
	if installer.options.Mode == ModeUninstall {
		return installer.editOpenCodeInstructions(false)
	}
	return installer.editOpenCodeInstructions(true)
}

func (installer *engine) editOpenCodeInstructions(wanted bool) error {
	path := strings.TrimSpace(installer.options.OpenCodeConfigPath)
	if path == "" {
		installer.skip("no OpenCode config path configured — prompt wiring has nothing to write")
		return nil
	}
	path = physicalSettingsPath(path)
	original, existed, err := readMCPFile(path)
	if err != nil {
		return fmt.Errorf("read OpenCode config %s: %w", path, err)
	}
	if !existed && !wanted {
		return nil
	}
	base := original
	if !existed {
		base = []byte("{}\n")
	}
	document, err := decodeJSONCObject(base)
	if err != nil {
		return fmt.Errorf("parse OpenCode config %s: %w", path, err)
	}
	staged := installer.stagedHarnessPromptPath(pfmengine.MustLookup(pfmengine.OpenCode).LongName)
	current, err := openCodeInstructionEntries(document, path)
	if err != nil {
		return err
	}
	next := make([]string, 0, len(current)+1)
	if wanted {
		next = append(next, staged)
	}
	for _, entry := range current {
		if entry == staged {
			continue
		}
		next = append(next, entry)
	}
	wantedRaw, err := renderOpenCodeInstructions(base, next)
	if err != nil {
		return fmt.Errorf("plan OpenCode config %s: %w", path, err)
	}
	if bytes.Equal(base, wantedRaw) {
		installer.ok(path + " OpenCode prompt wiring")
		return nil
	}
	return installer.change(changeDescription(path, existed), func() error {
		return installer.writeMCPFile(path, original, wantedRaw, existed)
	})
}

// openCodeInstructionEntries reads the existing array. A present-but-wrong
// shape is an error, never an empty list: silently replacing an operator's
// object or string there would delete instructions pfm never wrote.
func openCodeInstructionEntries(document map[string]any, path string) ([]string, error) {
	value, present := document[openCodeInstructionsKey]
	if !present || value == nil {
		return nil, nil
	}
	raw, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("OpenCode config %s: %s must be an array of file paths", path, openCodeInstructionsKey)
	}
	entries := make([]string, 0, len(raw))
	for index, item := range raw {
		entry, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf(
				"OpenCode config %s: %s[%d] must be a string",
				path,
				openCodeInstructionsKey,
				index,
			)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// renderOpenCodeInstructions edits only the top-level instructions property
// and leaves every comment and unowned property byte-for-byte intact. An
// empty result removes the property rather than leaving an empty array pfm
// put there.
func renderOpenCodeInstructions(raw []byte, entries []string) ([]byte, error) {
	if len(entries) == 0 {
		return removeJSONCProperty(raw, 0, openCodeInstructionsKey)
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	return setJSONCProperty(raw, 0, openCodeInstructionsKey, encoded)
}
