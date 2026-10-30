package installer

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// vscodeSystemPromptName is the Prompt Lint run's system prompt
// (prompt-lint.js passes it as --system-file), built at install into the
// staged extension and never tracked: prompt-lint.md, then the
// /quality:prompt law from the source clone.
const vscodeSystemPromptName = "prompt-lint.system.md"

// vscodeLintPromptAsset is the embedded half of the system prompt.
const vscodeLintPromptAsset = vscodeExtensionSource + "/prompt-lint.md"

func (installer *engine) vscodeSystemPromptPath() string {
	return filepath.Join(installer.managedRoot, filepath.FromSlash(vscodeExtensionSource), vscodeSystemPromptName)
}

// vscodeLawSource is /quality:prompt's source file in a Professor clone, the
// file the machine-global commands link in ~/.claude/commands resolves to.
func vscodeLawSource(repo string) string {
	return filepath.Join(repo, "templates", "global", "commands", "quality", "prompt.md")
}

// writeVSCodeSystemPrompt builds prompt-lint.system.md from prompt-lint.md and the
// law of the clone the install runs from; a law it cannot read fails the
// install by its path, never a system prompt without the law.
func (installer *engine) writeVSCodeSystemPrompt() error {
	repo, err := installer.globalSourceRepoRoot()
	if err != nil {
		return fmt.Errorf("build VS Code %s: resolve source repository: %w", vscodeSystemPromptName, err)
	}
	source := vscodeLawSource(repo)
	law, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("build VS Code %s: read the prompt law %s: %w", vscodeSystemPromptName, source, err)
	}
	lint, err := readAsset(vscodeLintPromptAsset)
	if err != nil {
		return fmt.Errorf("build VS Code %s: read embedded %s: %w", vscodeSystemPromptName, vscodeLintPromptAsset, err)
	}
	content := vscodeSystemPrompt(lint, law)
	target := installer.vscodeSystemPromptPath()
	if sameFile(target, content, 0o644) {
		installer.ok(target)
		return nil
	}
	return installer.change("write "+target, func() error {
		return atomicfile.Write(target, content, 0o644)
	})
}

// vscodeSystemPrompt joins prompt-lint.md and the law with one blank line,
// the law's YAML frontmatter (routing metadata) dropped; a CRLF law (a
// core.autocrlf checkout) reads as LF so its frontmatter drops too.
func vscodeSystemPrompt(lint, law []byte) []byte {
	law = bytes.ReplaceAll(law, []byte("\r\n"), []byte("\n"))
	if rest, ok := bytes.CutPrefix(law, []byte("---\n")); ok {
		if _, body, closed := bytes.Cut(rest, []byte("\n---\n")); closed {
			law = body
		}
	}
	return slices.Concat(bytes.TrimRight(lint, "\n"), []byte("\n\n"), bytes.TrimLeft(law, "\n"))
}

// removeVSCodeSystemPrompt deletes the built system prompt at uninstall, so
// removeManagedAssets can drop the extension's emptied directory.
func (installer *engine) removeVSCodeSystemPrompt() error {
	target := installer.vscodeSystemPromptPath()
	if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect VS Code %s: %w", target, err)
	}
	return installer.change("remove "+target, func() error { return os.Remove(target) })
}
