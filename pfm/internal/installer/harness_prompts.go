package installer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/codexprompt"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// codexHarnessPrompt applies the same Codex mappings used by role seats and doctor.
func codexHarnessPrompt() ([]byte, error) {
	prompt, err := codexprompt.FleetPrompt()
	if err != nil {
		return nil, err
	}
	return []byte(prompt), nil
}

func assembledHarnessPrompt(id pfmengine.ID) ([]byte, error) {
	if id == pfmengine.Codex {
		return codexHarnessPrompt()
	}
	return harnessprompts.Composed(pfmengine.MustLookup(id).LongName)
}

// generateHarnessPrompts runs before any install consumer. Only the parts are
// embedded, so a fresh clone builds without generated files. A dry run plans
// the same bytes without writing, and unchanged files keep their timestamps.
func (installer *engine) generateHarnessPrompts() error {
	repo, err := installer.globalSourceRepoRoot()
	if err != nil {
		return fmt.Errorf("resolve harness prompt source repository: %w", err)
	}
	for _, id := range pfmengine.All() {
		content, err := assembledHarnessPrompt(id)
		if err != nil {
			return fmt.Errorf("assemble %s harness prompt: %w", id, err)
		}
		if len(bytes.TrimSpace(content)) == 0 {
			return fmt.Errorf("assemble %s harness prompt: empty prompt", id)
		}
		target := paths.GeneratedHarnessPromptIn(repo, id)
		current, err := os.ReadFile(target)
		if err == nil && bytes.Equal(current, content) {
			installer.ok(target + " harness prompt")
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read generated harness prompt %s: %w", target, err)
		}
		if err := installer.change("generate harness prompt "+target, func() error {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("create generated harness prompt directory %s: %w", filepath.Dir(target), err)
			}
			if err := atomicfile.Write(target, content, 0o644); err != nil {
				return fmt.Errorf("write generated harness prompt %s: %w", target, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// generatedHarnessPrompt reads the file generation just wrote on apply. Plans
// use the future assembled bytes: a first install has no output to read yet.
func (installer *engine) generatedHarnessPrompt(id pfmengine.ID) ([]byte, error) {
	if !installer.apply {
		return assembledHarnessPrompt(id)
	}
	repo, err := installer.globalSourceRepoRoot()
	if err != nil {
		return nil, fmt.Errorf("resolve generated harness prompt: %w", err)
	}
	target := paths.GeneratedHarnessPromptIn(repo, id)
	content, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("read generated harness prompt %s: %w", target, err)
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return nil, fmt.Errorf("read generated harness prompt %s: empty prompt", target)
	}
	return content, nil
}
