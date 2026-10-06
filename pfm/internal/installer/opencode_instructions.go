package installer

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// openCodeInstructionsKey is the machine-scope config array OpenCode reads
// extra system-prompt files from. OpenCode has no --system-prompt-file and no
// appendix hook: `Instruction.systemPaths` resolves every entry of this array
// (absolute paths globbed, `~/` expanded) and `Instruction.system` reads each
// one into the session's system array, so naming the composed prompt here is
// the ONE door pfm has into an OpenCode system prompt.
const openCodeInstructionsKey = "instructions"

// wireOpenCodeInstructions points OpenCode's config at the clone's composed
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

	// The clone being installed wins: a first install records the marker
	// only later in this same run, as at wireShell.
	var composed string
	if wanted {
		var err error
		if repo := strings.TrimSpace(installer.options.SourceRepo); repo != "" {
			var content []byte
			content, err = paths.SourceRepoMarkerContent(repo)
			if err == nil {
				composed = paths.ComposedHarnessPromptIn(strings.TrimSpace(string(content)), pfmengine.OpenCode)
			}
		} else {
			composed, err = paths.ComposedHarnessPrompt(installer.options.Home, pfmengine.OpenCode)
		}
		if errors.Is(err, paths.ErrNoSourceRepoMarker) {
			installer.skip("skip opencode instructions: no source repo recorded")
			return nil
		}
		if err != nil {
			return fmt.Errorf("resolve OpenCode prompt: %w", err)
		}
	}
	path = physicalSettingsPath(path)
	original, existed, err := readMCPFile(path)
	if err != nil {
		return fmt.Errorf("read OpenCode config %s: %w", path, err)
	}
	base := original
	if !existed {
		base = []byte("{}\n")
	}
	document, err := decodeJSONCObject(base)
	if err != nil {
		return fmt.Errorf("parse OpenCode config %s: %w", path, err)
	}
	receiptPath := filepath.Join(managedRootForHome(installer.options.Home), "opencode-instructions.json")
	receipt, readErr := os.ReadFile(receiptPath)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return fmt.Errorf("read OpenCode instruction ownership %s: %w", receiptPath, readErr)
	}
	var owned openCodeInstructionOwnership
	if readErr == nil {
		if err := json.Unmarshal(receipt, &owned); err != nil {
			return fmt.Errorf("parse OpenCode instruction ownership %s: %w", receiptPath, err)
		}
	}
	intentPath := receiptPath + ".pending"
	intent, err := readOpenCodeInstructionIntent(intentPath)
	if err != nil {
		return err
	}
	if !wanted && !existed {
		if installer.apply {
			if err := removeOpenCodeInstructionIntent(intentPath); err != nil {
				return err
			}
			if owned.Config == path {
				if err := os.Remove(receiptPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf("remove OpenCode instruction ownership %s: %w", receiptPath, err)
				}
			}
		}
		return nil
	}
	if intent != nil {
		if intent.Ownership.Config != path {
			return fmt.Errorf("pending OpenCode ownership names another config: %s", intentPath)
		}
		fingerprint := fmt.Sprintf("%x", sha256.Sum256(base))
		switch fingerprint {
		case intent.After:
			owned = intent.Ownership
			if installer.apply {
				if err := publishOpenCodeInstructionOwnership(receiptPath, owned); err != nil {
					return err
				}
				if err := removeOpenCodeInstructionIntent(intentPath); err != nil {
					return err
				}
			}
		case intent.Before:
			if !wanted && installer.apply {
				if err := removeOpenCodeInstructionIntent(intentPath); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf(
				"OpenCode config changed during pending ownership publication: %s; reconcile %s before retry",
				path,
				intentPath,
			)
		}
	}
	legacy := filepath.Join(paths.LegacyHarnessPromptsDir(installer.options.Home), "opencode.md")
	current, err := openCodeInstructionEntries(document, path)
	if err != nil {
		return err
	}
	claimComposed := wanted &&
		((owned.Config == path && owned.Instruction == composed) || !slices.Contains(current, composed))
	next := make([]string, 0, len(current)+1)
	if wanted {
		next = append(next, composed)
	}
	for _, entry := range current {
		if entry == legacy || (owned.Config == path && entry == owned.Instruction) || (wanted && entry == composed) {
			continue
		}
		next = append(next, entry)
	}
	wantedRaw, err := renderOpenCodeInstructions(base, next)
	if err != nil {
		return fmt.Errorf("plan OpenCode config %s: %w", path, err)
	}
	if !bytes.Equal(base, wantedRaw) {
		if installer.apply && claimComposed {
			intent = &openCodeInstructionIntent{
				Ownership: openCodeInstructionOwnership{Config: path, Instruction: composed},
				Before:    fmt.Sprintf("%x", sha256.Sum256(base)), After: fmt.Sprintf("%x", sha256.Sum256(wantedRaw)),
			}
			content, err := json.Marshal(intent)
			if err != nil {
				return fmt.Errorf("encode OpenCode ownership intent: %w", err)
			}
			if err := atomicfile.Write(intentPath, content, 0o600); err != nil {
				return fmt.Errorf("write OpenCode ownership intent %s: %w", intentPath, err)
			}
		}
		if err := installer.changeMCPFile(
			changeDescription(path, existed),
			path,
			original,
			wantedRaw,
			existed,
		); err != nil {
			return err
		}
	} else {
		installer.ok(path + " OpenCode prompt wiring")
	}
	if !installer.apply {
		return nil
	}
	if wanted {
		if !claimComposed {
			return nil
		}
		owned = openCodeInstructionOwnership{Config: path, Instruction: composed}
		if err := publishOpenCodeInstructionOwnership(receiptPath, owned); err != nil {
			return err
		}
		return removeOpenCodeInstructionIntent(intentPath)
	}
	if owned.Config == path {
		if err := os.Remove(receiptPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove OpenCode instruction ownership %s: %w", receiptPath, err)
		}
	}
	return nil
}

type openCodeInstructionOwnership struct {
	Config      string `json:"config"`
	Instruction string `json:"instruction"`
}
type openCodeInstructionIntent struct {
	Ownership openCodeInstructionOwnership `json:"ownership"`
	Before    string                       `json:"before"`
	After     string                       `json:"after"`
}

func readOpenCodeInstructionIntent(path string) (*openCodeInstructionIntent, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read OpenCode ownership intent %s: %w", path, err)
	}
	var intent openCodeInstructionIntent
	if err := json.Unmarshal(raw, &intent); err != nil {
		return nil, fmt.Errorf("parse OpenCode ownership intent %s: %w", path, err)
	}
	if intent.Before == "" || intent.After == "" || intent.Ownership.Config == "" ||
		intent.Ownership.Instruction == "" {
		return nil, fmt.Errorf("invalid OpenCode ownership intent %s", path)
	}
	return &intent, nil
}

func publishOpenCodeInstructionOwnership(path string, owned openCodeInstructionOwnership) error {
	raw, err := json.Marshal(owned)
	if err != nil {
		return fmt.Errorf("encode OpenCode instruction ownership: %w", err)
	}
	if err := atomicfile.Write(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write OpenCode instruction ownership %s: %w", path, err)
	}
	return nil
}

func removeOpenCodeInstructionIntent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove OpenCode ownership intent %s: %w", path, err)
	}
	return nil
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
