package installer

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// harnessPromptsDirName is the asset-path prefix the parts come from and the
// managed-root directory the composed prompts stage into — the embedding
// package's own name, so a template part and its staged prompt always sit in
// the same place.
const harnessPromptsDirName = harnessprompts.DirName

// harnessPromptReadme is the tree's human-facing page. It is embedded with
// the parts so doctor can compare the binary's tree against the clone's
// whole, and held back HERE so it never stages into an operator's managed
// root — the one place that rule is written.
const harnessPromptReadme = "README.md"

// harnessPromptAssetName splits a harness-prompt asset path into its
// tree-relative name; anything else is an asset of this package's own
// embedded tree.
func harnessPromptAssetName(asset string) (string, bool) {
	return strings.CutPrefix(asset, harnessPromptsDirName+"/")
}

// harnessPromptAssetFiles lists the embedded harness-prompt parts as staged
// assets. The tree lives in its own package, so assetFiles' walk over
// embeddedAssets cannot see it — without this the parts would never stage,
// and both doctor's baseline row and the Codex hook file read a staged part.
func harnessPromptAssetFiles() ([]assetFile, error) {
	var files []assetFile
	if err := fs.WalkDir(harnessprompts.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk embedded harness prompts at %s: %w", name, err)
		}
		if entry.IsDir() || name == harnessPromptReadme {
			return nil
		}
		files = append(files, assetFile{path: path.Join(harnessPromptsDirName, name), mode: 0o644})
		return nil
	}); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("embedded harness prompt tree holds no parts")
	}
	return files, nil
}

// harnessPromptEngines is the closed set of engines the fleet composes a
// system prompt for. An engine's middle is <long name>/professor.md in the
// embedded tree and its composed prompt stages as <long name>.md, which is
// exactly what paths.HarnessPromptPath hands the three readers.
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
