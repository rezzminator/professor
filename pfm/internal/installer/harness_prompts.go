package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/codexgen"
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

// codexHarnessPrompt is the composed Codex fleet prompt — the same bytes
// staged as harness-prompts/codex.md, written into every Codex home's
// developer_instructions and prepended to every compiled Codex role. It comes
// from codexgen, the one door that composes it AND applies the Codex mappings
// the shared parts deliberately do not carry on disk; composing it here from
// the parts would stage a file doctor reads as drift.
func codexHarnessPrompt() ([]byte, error) {
	prompt, err := codexgen.FleetPrompt()
	if err != nil {
		return nil, err
	}
	return []byte(prompt), nil
}

// composedHarnessPrompt returns one engine's composed prompt: Codex's through
// codexgen, every other engine's straight from the embedded parts.
func composedHarnessPrompt(id pfmengine.ID) ([]byte, error) {
	if id == pfmengine.Codex {
		return codexHarnessPrompt()
	}
	return harnessprompts.Composed(pfmengine.MustLookup(id).LongName)
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
		content, err := composedHarnessPrompt(id)
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
