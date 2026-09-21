package installer

import (
	"bytes"
	"fmt"
	"path"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// harnessPromptsDirName is the embedded asset tree the parts come from and
// the managed-root directory the composed prompts stage into — one name, so
// a template part and its staged prompt always sit in the same place.
const harnessPromptsDirName = "harness-prompts"

// harnessPromptEngines is the closed set of engines the fleet composes a
// system prompt for. An engine's middle is
// assets/harness-prompts/<long name>/professor.md and its composed prompt
// stages as <long name>.md, which is exactly what paths.HarnessPromptPath
// hands the three readers.
var harnessPromptEngines = []pfmengine.ID{pfmengine.Claude, pfmengine.Codex, pfmengine.OpenCode}

// harnessPromptParts names the three embedded assets one engine's prompt is
// composed from, head first and tail last.
func harnessPromptParts(long string) [3]string {
	return [3]string{
		path.Join(harnessPromptsDirName, "share", "head.md"),
		path.Join(harnessPromptsDirName, long, "professor.md"),
		path.Join(harnessPromptsDirName, "share", "tail.md"),
	}
}

// composeHarnessPrompt joins the shared head, one engine's middle and the
// shared tail. Each part's trailing newlines are trimmed and the three are
// joined by a single blank line, so a part that gains or loses a trailing
// newline cannot move the seams — the staged file is the three parts and the
// two seams, and nothing else.
func composeHarnessPrompt(head, middle, tail []byte) []byte {
	parts := [][]byte{
		bytes.TrimRight(head, "\n"),
		bytes.TrimRight(middle, "\n"),
		bytes.TrimRight(tail, "\n"),
	}
	return append(bytes.Join(parts, []byte("\n\n")), '\n')
}

// composeHarnessPromptAsset composes one engine's prompt from the embedded
// parts. Composition happens HERE, at stage time — an engine reads a file on
// disk, never an assembly its launcher had to get right.
func composeHarnessPromptAsset(long string) ([]byte, error) {
	var parts [3][]byte
	for index, name := range harnessPromptParts(long) {
		raw, err := readAsset(name)
		if err != nil {
			return nil, fmt.Errorf("read harness prompt part %s: %w", name, err)
		}
		parts[index] = raw
	}
	return composeHarnessPrompt(parts[0], parts[1], parts[2]), nil
}

// stagedHarnessPromptPath is where one engine's composed prompt lands under
// this run's managed root.
func (installer *engine) stagedHarnessPromptPath(long string) string {
	return filepath.Join(installer.managedRoot, harnessPromptsDirName, long+".md")
}

// stageHarnessPrompts writes one composed prompt per engine beside the parts
// stageAssets has just staged.
func (installer *engine) stageHarnessPrompts() error {
	installer.say("harness prompts -> %s", filepath.Join(installer.managedRoot, harnessPromptsDirName))
	for _, id := range harnessPromptEngines {
		long := pfmengine.MustLookup(id).LongName
		content, err := composeHarnessPromptAsset(long)
		if err != nil {
			return err
		}
		target := installer.stagedHarnessPromptPath(long)
		if sameFile(target, content, 0o644) {
			installer.ok(target)
			continue
		}
		if err := installer.change("write "+target, func() error {
			return atomicfile.Write(target, content, 0o644)
		}); err != nil {
			return err
		}
	}
	installer.say("")
	return nil
}

// removeStagedHarnessPrompts drops the composed prompts on uninstall. The
// parts they were composed from are embedded assets and leave with
// removeManagedAssets; these three are written by this package, so nothing
// else would ever take them away.
func (installer *engine) removeStagedHarnessPrompts() error {
	for _, id := range harnessPromptEngines {
		target := installer.stagedHarnessPromptPath(pfmengine.MustLookup(id).LongName)
		if err := installer.retire(target, "composed harness prompt"); err != nil {
			return err
		}
	}
	return nil
}
